package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

const linkWait = 10 * time.Second

// linkHarness is a hosted Session bound to h.device whose Environment has an
// allocation with a Serve credential, and Core's Link Authority behind a
// relay.
type linkHarness struct {
	*dispatchHarness
	relay    *sandboxlinktest.Server
	resource sandboxbootstrap.Resource
	serve    []byte
}

// newLinkHarness binds the Session to an unmarked operator device, or, for a
// guest, to the allocation's own device, as an in-sandbox daemon is bound.
func newLinkHarness(t *testing.T, guest bool) *linkHarness {
	t.Helper()
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`), guest)
	device := h.device.ID
	if !guest {
		allocated, err := sessionService(t, h.s).CreateDevice(t.Context(), h.tenant, "sandbox", runtimedevice.HashCredential(uuid.NewString()))
		if err != nil {
			t.Fatal(err)
		}
		device = allocated.ID
	}
	l := &linkHarness{dispatchHarness: h, relay: sandboxlinktest.StartRelay(t, runtimegateway.NewLinkAuthority(sessionAdapter(h.s))), serve: []byte(uuid.NewString()),
		resource: sandboxbootstrap.Resource{TenantID: h.tenant, EnvironmentID: h.session.Environment.ID, Kind: "allocation", ID: uuid.NewString(), Generation: 1}}
	if _, err := h.s.pool.Exec(t.Context(), `INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key,state,create_settled,deployment_generation,serve_credential_hash)
		VALUES($1,$2,$3,$4,'running',true,(SELECT generation FROM runtime_deployment),$5)`, l.resource.ID, l.resource.EnvironmentID, device, uuid.NewString(), serveHash(l.serve)); err != nil {
		t.Fatal(err)
	}
	return l
}

func serveHash(credential []byte) string {
	digest := sha256.Sum256(credential)
	return hex.EncodeToString(digest[:])
}

func (l *linkHarness) exec(query string, args ...any) {
	l.t.Helper()
	if _, err := l.s.pool.Exec(l.t.Context(), query, args...); err != nil {
		l.t.Fatal(err)
	}
}

// bind binds the Session's current assignment to h.device through Core's
// gateway and returns it with the payload the Runtime received.
func (l *linkHarness) bind() (proto.AssignmentRef, proto.AssignmentBindPayload) {
	t := l.t
	t.Helper()
	ref := proto.AssignmentRef{SessionID: l.session.ID}
	var epoch int64
	if err := l.s.pool.QueryRow(t.Context(), "SELECT assignment_id::text, epoch FROM session_runtime_assignments WHERE session_id=$1", l.session.ID).Scan(&ref.AssignmentID, &epoch); err != nil {
		t.Fatal(err)
	}
	ref.Epoch = uint64(epoch)
	peer, err := l.registry.LookupDevice(l.device.ID)
	if err != nil {
		t.Fatal(err)
	}
	bound := make(chan error, 1)
	go func() { bound <- peer.Bind(t.Context(), ref, l.session.Environment.ID) }()
	frame := l.read(proto.TypeAssignmentBind)
	var payload proto.AssignmentBindPayload
	if err := frame.DecodePayload(&payload); err != nil {
		t.Fatal(err)
	}
	reply, _ := assignmentReply(frame)
	l.writeMu.Lock()
	err = l.conn.WriteJSON(reply)
	l.writeMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := within(t, bound); err != nil {
		t.Fatal(err)
	}
	return ref, payload
}

func (l *linkHarness) attach(runtime string, credential []byte) (*sandboxlink.AttachLink, error) {
	return attachLink(l.t, l.relay, runtime, credential)
}

func attachLink(t *testing.T, srv *sandboxlinktest.Server, runtime string, credential []byte) (*sandboxlink.AttachLink, error) {
	ctx, cancel := context.WithTimeout(t.Context(), linkWait)
	defer cancel()
	link, err := sandboxlink.DialAttach(ctx, sandboxlink.AttachConfig{URL: srv.URL, TLS: srv.TLS, RuntimeID: wireID(runtime), Credential: credential})
	if err == nil {
		t.Cleanup(func() { link.Close() })
	}
	return link, err
}

// open opens service on a new attachment of the harness's resource under ref.
func (l *linkHarness) open(link *sandboxlink.AttachLink, service sandboxlink.Service, ref proto.AssignmentRef, grant []byte) (sandboxwire.ID, error) {
	ctx, cancel := context.WithTimeout(l.t.Context(), linkWait)
	defer cancel()
	attachment := sandboxwire.NewID()
	stream, _, err := link.OpenService(ctx, sandboxlink.Open{Service: service, Version: 1, Resource: l.resource.Ref(), AttachmentID: attachment,
		SessionID: wireID(ref.SessionID), AssignmentID: wireID(ref.AssignmentID), AssignmentEpoch: ref.Epoch, AttachGrant: grant})
	if err == nil {
		l.t.Cleanup(func() { stream.Reset() })
	}
	return attachment, err
}

func (l *linkHarness) renew(link *sandboxlink.AttachLink, attachment sandboxwire.ID, grant []byte) error {
	ctx, cancel := context.WithTimeout(l.t.Context(), linkWait)
	defer cancel()
	_, err := link.Renew(ctx, sandboxlink.RenewAttachment{AttachmentID: attachment, AttachGrant: grant})
	return err
}

// linkServe is a serve peer of File and Network whose handlers hold their
// streams until the attachment closes.
type linkServe struct {
	connected chan struct{}
	binds     chan sandboxlink.Bind
	done      chan struct{}
	err       error // Serve's result once done is closed
	cancel    context.CancelFunc
}

func startLinkServe(t *testing.T, srv *sandboxlinktest.Server, credential []byte, resource sandboxlink.ResourceRef) *linkServe {
	p := &linkServe{connected: make(chan struct{}, 16), binds: make(chan sandboxlink.Bind, 16), done: make(chan struct{})}
	hold := func(ctx context.Context, b sandboxlink.Bind, _ uint64, _ sandboxlink.Stream) {
		p.binds <- b
		<-ctx.Done()
	}
	var ctx context.Context
	ctx, p.cancel = context.WithCancel(context.Background())
	go func() {
		defer close(p.done)
		p.err = sandboxlink.Serve(ctx, sandboxlink.ServeConfig{URL: srv.URL, TLS: srv.TLS, Credential: credential, Resource: resource, ServerInstanceID: sandboxwire.NewID(),
			Services:    []sandboxlink.ServiceHandler{{Service: sandboxlink.ServiceFile, Version: 1, Serve: hold}, {Service: sandboxlink.ServiceNetwork, Version: 1, Serve: hold}},
			OnConnected: func() { p.connected <- struct{}{} }, MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	}()
	t.Cleanup(p.stop)
	return p
}

func (p *linkServe) stop() {
	p.cancel()
	<-p.done
}

// refused returns the code of the failure that ended Serve.
func (p *linkServe) refused(t *testing.T) sandboxlink.Code {
	t.Helper()
	within(t, p.done)
	return linkCode(p.err)
}

func linkCode(err error) sandboxlink.Code {
	var failure *sandboxlink.Error
	if !errors.As(err, &failure) {
		return 0
	}
	return failure.Code
}

func wireID(id string) sandboxwire.ID { return sandboxwire.ID(uuid.MustParse(id)) }

func within[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(linkWait):
		t.Fatal("timed out")
		panic("unreachable")
	}
}

// TestLinkAuthorityAgentHost serves an allocation and opens services on it
// from a marked agent host, and checks that each part of the grant's
// authority is current at every Open and renewal.
func TestLinkAuthorityAgentHost(t *testing.T) {
	l := newLinkHarness(t, false)
	p := startLinkServe(t, l.relay, l.serve, l.resource.Ref())
	within(t, p.connected)

	otherID, otherEnvironment := l.resource, l.resource
	otherID.ID, otherEnvironment.EnvironmentID = uuid.NewString(), uuid.NewString()
	for _, test := range []struct {
		resource sandboxbootstrap.Resource
		want     sandboxlink.Code
	}{{otherID, sandboxlink.AuthenticationFailed}, {otherEnvironment, sandboxlink.PermissionDenied}} {
		if got := startLinkServe(t, l.relay, l.serve, test.resource.Ref()).refused(t); got != test.want {
			t.Fatalf("Serve of another resource refused with %v, want %v", got, test.want)
		}
	}
	if _, err := l.attach(l.resource.ID, l.serve); linkCode(err) != sandboxlink.AuthenticationFailed {
		t.Fatal("a Serve credential attached", err)
	}
	if _, err := l.attach(l.device.ID, []byte(l.credential)); linkCode(err) != sandboxlink.AuthenticationFailed {
		t.Fatal("an unmarked device attached", err)
	}

	l.exec("UPDATE devices SET agent_host = true WHERE id = $1", l.device.ID)
	ref, payload := l.bind()
	if payload.Resource == nil || *payload.Resource != l.resource || len(payload.AttachGrant) == 0 {
		t.Fatalf("agent host bind = %+v", payload)
	}
	grant := payload.AttachGrant
	link, err := l.attach(l.device.ID, []byte(l.credential))
	if err != nil {
		t.Fatal(err)
	}
	file, err := l.open(link, sandboxlink.ServiceFile, ref, grant)
	if err != nil {
		t.Fatal(err)
	}
	if b := within(t, p.binds); len(b.Exports) != 1 || b.Exports[0] != (sandboxlink.ExportGrant{ID: sandboxfs.WorldExport}) {
		t.Fatalf("File bind exports %+v", b.Exports)
	}
	if _, err := l.open(link, sandboxlink.ServiceNetwork, ref, grant); err != nil {
		t.Fatal(err)
	}
	if b := within(t, p.binds); len(b.Egress) != 0 {
		t.Fatalf("Network bind egress %+v with network access disabled", b.Egress)
	}
	if err := l.renew(link, file, grant); err != nil {
		t.Fatal(err)
	}

	wrong := bytes.Clone(grant)
	wrong[len(wrong)-1] ^= 1
	if _, err := l.open(link, sandboxlink.ServiceFile, ref, wrong); linkCode(err) != sandboxlink.PermissionDenied {
		t.Fatal("wrong grant", err)
	}
	l.exec("UPDATE devices SET credential_revision = credential_revision + 1 WHERE id = $1", l.device.ID)
	if _, err := l.open(link, sandboxlink.ServiceFile, ref, grant); linkCode(err) != sandboxlink.AuthenticationFailed {
		t.Fatal("stale credential revision", err)
	}
	if link, err = l.attach(l.device.ID, []byte(l.credential)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.open(link, sandboxlink.ServiceFile, ref, grant); err != nil {
		t.Fatal("current credential revision", err)
	}
	l.exec("UPDATE session_runtime_assignments SET epoch = epoch + 1 WHERE session_id = $1", l.session.ID)
	if _, err := l.open(link, sandboxlink.ServiceFile, ref, grant); linkCode(err) != sandboxlink.StaleAssignment {
		t.Fatal("stale epoch", err)
	}
	next, payload := l.bind()
	if _, err := l.open(link, sandboxlink.ServiceFile, next, payload.AttachGrant); err != nil {
		t.Fatal("current epoch", err)
	}
	l.exec("UPDATE runtime_allocations SET serve_generation = 2 WHERE id = $1", l.resource.ID)
	if _, err := l.open(link, sandboxlink.ServiceFile, next, payload.AttachGrant); linkCode(err) != sandboxlink.StaleGeneration {
		t.Fatal("stale generation", err)
	}
}

// TestLinkAuthorityGuest checks that an in-sandbox daemon's assignment
// carries no grant and that its device can neither attach nor be marked.
func TestLinkAuthorityGuest(t *testing.T) {
	l := newLinkHarness(t, true)
	if _, payload := l.bind(); payload.Resource != nil || payload.AttachGrant != nil {
		t.Fatalf("guest bind = %+v", payload)
	}
	if _, err := l.attach(l.device.ID, []byte(l.credential)); linkCode(err) != sandboxlink.AuthenticationFailed {
		t.Fatal("a guest attached", err)
	}
	if _, err := l.s.pool.Exec(t.Context(), "UPDATE devices SET agent_host = true WHERE id = $1", l.device.ID); err == nil || !strings.Contains(err.Error(), "devices_agent_host") {
		t.Fatal("a guest device was marked as an agent host", err)
	}
}

// TestLinkAuthorityReleaseRevokesBeforeSend checks that a released
// assignment's grant opens nothing from the commit on, and that the Worker
// has the relay close its attachments before it sends the release.
func TestLinkAuthorityReleaseRevokesBeforeSend(t *testing.T) {
	l := newLinkHarness(t, false)
	p := startLinkServe(t, l.relay, l.serve, l.resource.Ref())
	within(t, p.connected)
	l.exec("UPDATE devices SET agent_host = true WHERE id = $1", l.device.ID)
	ref, payload := l.bind()
	link, err := l.attach(l.device.ID, []byte(l.credential))
	if err != nil {
		t.Fatal(err)
	}
	file, err := l.open(link, sandboxlink.ServiceFile, ref, payload.AttachGrant)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionService(t, l.s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: l.tenant, SessionID: l.session.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.open(link, sandboxlink.ServiceFile, ref, payload.AttachGrant); linkCode(err) != sandboxlink.ResourceNotFound {
		t.Fatal("a released assignment opened a service", err)
	}
	dispatcher := *l.d
	dispatcher.Links = l.relay.Relay
	worker := startWorker(t, t.Context(), l.s, &dispatcher)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	if release := l.read(proto.TypeAssignmentRelease); release.Assignment.AssignmentID != ref.AssignmentID || release.Assignment.Epoch != ref.Epoch+1 {
		t.Fatal("release of", release.Assignment)
	}
	// The relay no longer holds the attachment, so the revocation preceded the release.
	if err := l.renew(link, file, payload.AttachGrant); linkCode(err) != sandboxlink.LeaseExpired {
		t.Fatal("the release reached the Runtime before the relay closed its attachment", err)
	}
}

// TestLinkAuthorityEnrollment serves a self_hosted enrollment with its
// executor key until the key is revoked.
func TestLinkAuthorityEnrollment(t *testing.T) {
	s, _ := NewModelTestStore(t)
	principal := FixtureExecutorPrincipal(t, s, uuid.NewString())
	session, err := s.CreateSession(t.Context(), principal.TenantID, sessions.CreateSession{
		Creator: principal.Subject(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := sessionService(t, s).IssueExecutorCredential(t.Context(), principal, uuid.NewString(), session.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	resource := sandboxbootstrap.Resource{TenantID: principal.TenantID, EnvironmentID: session.Environment.ID, Kind: "enrollment", ID: uuid.NewString(), Generation: 1}
	if _, err := s.pool.Exec(t.Context(), "INSERT INTO sandbox_enrollments(id, environment_id, executor_key_id) VALUES($1, $2, $3)", resource.ID, resource.EnvironmentID, key.KeyID); err != nil {
		t.Fatal(err)
	}
	srv := sandboxlinktest.StartRelay(t, runtimegateway.NewLinkAuthority(sessionAdapter(s)))
	served := startLinkServe(t, srv, []byte(key.Token), resource.Ref())
	within(t, served.connected)
	served.stop()
	if err := sessionService(t, s).RevokeExecutorCredential(t.Context(), principal, key.KeyID); err != nil {
		t.Fatal(err)
	}
	if got := startLinkServe(t, srv, []byte(key.Token), resource.Ref()).refused(t); got != sandboxlink.AuthenticationFailed {
		t.Fatal("a revoked executor key served", got)
	}
}

// TestLinkAuthorityDestroyedAllocation checks that the Worker's cleanup of an
// allocation withdraws its Serve authority and then disconnects its serve
// peer, which cannot serve again.
func TestLinkAuthorityDestroyedAllocation(t *testing.T) {
	s, _ := newManagedTestStore(t)
	tenant, session, environment := managedSession(t, s)
	key := uuid.NewString()
	srv := sandboxlinktest.StartRelay(t, runtimegateway.NewLinkAuthority(sessionAdapter(s)))
	w := startWorker(t, t.Context(), s, &execution.Dispatcher{Registry: runtimegateway.NewRegistry(), Links: srv.Relay, ManagedRuntimes: &execution.RuntimeProvider{
		CoreURL: "http://core.invalid/api/v1", InstallationID: key, BackendFingerprint: strings.Repeat("a", 64), Provider: &lifecycleProvider{resources: map[string]sandbox.Info{}}}})
	t.Cleanup(func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); _ = w.Run(ctx) })
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	credential := []byte(uuid.NewString())
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET serve_credential_hash = $2 WHERE id = $1", owner.ID, serveHash(credential)); err != nil {
		t.Fatal(err)
	}
	p := startLinkServe(t, srv, credential, sandboxbootstrap.Resource{TenantID: tenant, EnvironmentID: environment.ID, Kind: "allocation", ID: owner.ID, Generation: 1}.Ref())
	within(t, p.connected)
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, environment.ID, "released")
	if got := p.refused(t); got != sandboxlink.AuthenticationFailed {
		t.Fatal("a destroyed allocation's Serve credential served", got)
	}
}
