package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/processconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtime"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

const linkWait = 10 * time.Second

// linkHarness is a dispatch harness whose Worker runs with the harness's
// relay: the Session is bound to the agent host, and its Environment's Link
// resource Serves at the relay.
type linkHarness struct {
	*dispatchHarness
}

const hostedLinkSession = `{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`

func newLinkHarness(t *testing.T, configuration string) *linkHarness {
	t.Helper()
	h := newDispatchHarnessForSession(t, []byte(configuration))
	runWorker(t, startWorker(t, t.Context(), h.s, h.d))
	return &linkHarness{dispatchHarness: h}
}

// insertAllocation inserts a running resource that Serves with the credential.
func insertAllocation(t *testing.T, s *Store, resource sandboxbootstrap.Resource, serve []byte) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO runtime_allocations(id,environment_id,provider_key,state,create_settled,deployment_generation,serve_credential_hash)
		VALUES($1,$2,(SELECT installation_id FROM runtime_deployment),'running',true,(SELECT generation FROM runtime_deployment),$3)`, resource.ID, resource.EnvironmentID, serveHash(serve)); err != nil {
		t.Fatal(err)
	}
}

// runWorker runs w until the test ends.
func runWorker(t *testing.T, w *execution.Worker) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

// startLinkRoute serves Core's Link Authority at the Link route of the API
// handler on s, on an httptest TLS server, and dials it at the Link URL its
// origin derives. The relay closes before the server when the test ends.
func startLinkRoute(t *testing.T, s *Store) *sandboxlinktest.Server {
	t.Helper()
	rl := relay.New(runtimegateway.NewLinkAuthority(sessionAdapter(s)))
	handler, err := publicHandler(t, s, fixtureKeyResolver{}, "codex", func(d *api.Dependencies) { d.Execution.Links = rl })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { rl.Close() })
	origin, err := deployment.NewPublicOrigin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	link, err := origin.SandboxLink()
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	return &sandboxlinktest.Server{URL: link, TLS: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, Relay: rl}
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

// bind has the Worker bind the Session's current assignment to the agent host
// and returns it with the payload the agent host received. It reopens the
// Environment's initialization, which has nothing to install and binds first;
// the Worker claims it only while the Environment's resource is Serving.
func (l *linkHarness) bind() (proto.AssignmentRef, proto.AssignmentBindPayload) {
	t := l.t
	t.Helper()
	l.exec("UPDATE environments SET initialization = 'pending' WHERE id = $1", l.session.Environment.ID)
	frame := l.read(proto.TypeAssignmentBind)
	var payload proto.AssignmentBindPayload
	if err := frame.DecodePayload(&payload); err != nil {
		t.Fatal(err)
	}
	reply, _ := assignmentReply(frame)
	l.writeMu.Lock()
	err := l.conn.WriteJSON(reply)
	l.writeMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	awaitInitialization(t, l.s, l.tenant, l.session.Environment.ID, "complete")
	return frame.Assignment, payload
}

func (l *linkHarness) attach(runtime string, credential []byte) (*sandboxlink.AttachLink, error) {
	return attachLink(l.t, l.link, runtime, credential)
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
// from an agent host, and checks that each part of the grant's
// authority is current at every Open and renewal.
func TestLinkAuthorityAgentHost(t *testing.T) {
	l := newLinkHarness(t, hostedLinkSession)
	p := l.served

	otherID, otherEnvironment := l.resource, l.resource
	otherID.ID, otherEnvironment.EnvironmentID = uuid.NewString(), uuid.NewString()
	for _, test := range []struct {
		resource sandboxbootstrap.Resource
		want     sandboxlink.Code
	}{{otherID, sandboxlink.AuthenticationFailed}, {otherEnvironment, sandboxlink.PermissionDenied}} {
		if got := startLinkServe(t, l.link, l.serve, test.resource.Ref()).refused(t); got != test.want {
			t.Fatalf("Serve of another resource refused with %v, want %v", got, test.want)
		}
	}
	link, err := l.attach(l.device.ID, []byte(l.credential))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.attach(l.device.ID, l.serve); linkCode(err) != sandboxlink.AuthenticationFailed {
		t.Fatal("a Serve credential attached", err)
	}
	ref, payload := l.bind()
	if payload.Resource == nil || *payload.Resource != l.resource || len(payload.AttachGrant) == 0 {
		t.Fatalf("agent host bind = %+v", payload)
	}
	grant := payload.AttachGrant
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

// TestLinkAuthorityReleaseRevokesBeforeSend checks that a released
// assignment's grant opens nothing from the commit on, and that the Worker
// has the relay close its attachments before it sends the release.
func TestLinkAuthorityReleaseRevokesBeforeSend(t *testing.T) {
	l := newLinkHarness(t, hostedLinkSession)
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
	s, _ := testStore(t)
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
	srv := startLinkRoute(t, s)
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

// TestLinkAuthorityEnrollmentRotation checks that rotating an executor key
// advances its enrollment's generation and the epoch of the Session's bound
// assignment. No Open or renewal for the old generation is authorized, the
// Worker's pass ends the old secret's Serve, and after the machine re-enrolls
// and Serves the new generation, the agent host's next bind opens on it.
func TestLinkAuthorityEnrollmentRotation(t *testing.T) {
	l := newLinkHarness(t, `{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)
	p := l.served
	ref, payload := l.bind()
	if payload.Resource == nil || *payload.Resource != l.resource || len(payload.AttachGrant) == 0 {
		t.Fatalf("agent host bind = %+v", payload)
	}
	link, err := l.attach(l.device.ID, []byte(l.credential))
	if err != nil {
		t.Fatal(err)
	}
	file, err := l.open(link, sandboxlink.ServiceFile, ref, payload.AttachGrant)
	if err != nil {
		t.Fatal(err)
	}
	within(t, p.binds)

	var key string
	if err := l.s.pool.QueryRow(t.Context(), "SELECT executor_key_id::text FROM sandbox_enrollments WHERE id = $1", l.resource.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	principal := FixtureExecutorPrincipal(t, l.s, l.tenant)
	rotated, err := sessionService(t, l.s).RotateExecutorCredential(t.Context(), principal, key)
	if err != nil {
		t.Fatal(err)
	}
	var epoch uint64
	if err := l.s.pool.QueryRow(t.Context(), "SELECT epoch FROM session_runtime_assignments WHERE session_id = $1", l.session.ID).Scan(&epoch); err != nil || epoch != ref.Epoch+1 {
		t.Fatal("rotation left the assignment at epoch", epoch, err)
	}
	if _, err := l.open(link, sandboxlink.ServiceFile, ref, payload.AttachGrant); err == nil {
		t.Fatal("an Open reached the old secret's serve peer")
	}
	if err := l.renew(link, file, payload.AttachGrant); err == nil {
		t.Fatal("an attachment to the old secret's serve peer renewed")
	}
	if got := p.refused(t); got != sandboxlink.AuthenticationFailed {
		t.Fatal("the old secret's Serve ended with", got)
	}
	resource, err := sessionService(t, l.s).EnrollRuntime(t.Context(), l.resource.EnvironmentID, runtimedevice.HashCredential(rotated.Token))
	if next := l.resource; err != nil || resource != func() sandboxbootstrap.Resource { next.Generation++; return next }() {
		t.Fatalf("re-enrollment = %+v %v", resource, err)
	}
	l.resource = resource
	within(t, startLinkServe(t, l.link, []byte(rotated.Token), resource.Ref()).connected)
	next, payload := l.bind()
	if next.Epoch != ref.Epoch+1 || payload.Resource == nil || *payload.Resource != resource {
		t.Fatalf("bind after rotation = %+v %+v", next, payload)
	}
	if _, err := l.open(link, sandboxlink.ServiceFile, next, payload.AttachGrant); err != nil {
		t.Fatal("the new generation did not open", err)
	}
}

// TestLinkAuthorityDestroyedAllocation checks that the Worker's cleanup of an
// allocation withdraws its Serve authority and then disconnects its serve
// peer, which cannot serve again.
func TestLinkAuthorityDestroyedAllocation(t *testing.T) {
	s, _ := newManagedTestStore(t)
	key := webDeployment(t, s, "e2b")
	tenant, session, environment := managedSession(t, s)
	srv := startLinkRoute(t, s)
	provider := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	w := startWorker(t, t.Context(), s, &execution.Dispatcher{Registry: runtimegateway.NewRegistry(), Links: srv.Relay, ManagedRuntimes: webRuntimes(t, s, key, provider, nil)})
	t.Cleanup(func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); _ = w.Run(ctx) })
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	// The sandbox Serves with the input provisioning minted for it.
	if provider.serve.Resource != (sandboxbootstrap.Resource{TenantID: tenant, EnvironmentID: environment.ID, Kind: "allocation", ID: owner.ID, Generation: 1}) || provider.serve.LinkURL != "wss://core.invalid/api/v1/sandbox-link" {
		t.Fatalf("Create input: %+v %s", provider.serve.Resource, provider.serve.LinkURL)
	}
	p := startLinkServe(t, srv, []byte(provider.serve.Credential), provider.serve.Resource.Ref())
	within(t, p.connected)
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, environment.ID, "released")
	if got := p.refused(t); got != sandboxlink.AuthenticationFailed {
		t.Fatal("a destroyed allocation's Serve credential served", got)
	}
}

// TestRegisteredAgentHostAuthenticates registers the agent host from its
// identity file as Core's startup does. The Link route and the Runtime gateway
// accept its credential, a second startup changes nothing, and rotation fences
// the previous credential.
func TestRegisteredAgentHostAuthenticates(t *testing.T) {
	s, _ := newManagedTestStore(t)
	dir := t.TempDir()
	runtime, credential := uuid.NewString(), uuid.NewString()
	identity, _ := json.Marshal(map[string]string{"runtime_id": runtime, "credential": credential})
	for name, content := range map[string][]byte{
		"identity.json":   identity,
		"digests.json":    []byte(`["` + strings.Repeat("ab", 32) + `"]`),
		"installation.id": []byte(uuid.NewString()),
		"credential.key":  []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x91}, 32))),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OAC_DATABASE_URL", "postgres://core@database/core")
	t.Setenv("OAC_CORE_KEY_DIGESTS_FILE", filepath.Join(dir, "digests.json"))
	t.Setenv("OAC_PUBLIC_URL", "https://core.example")
	t.Setenv("OAC_INSTALLATION_ID_FILE", filepath.Join(dir, "installation.id"))
	t.Setenv("OAC_CREDENTIAL_KEY_FILE", filepath.Join(dir, "credential.key"))
	t.Setenv("OAC_AGENT_HOST_IDENTITY_FILE", filepath.Join(dir, "identity.json"))
	config, err := processconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	row := func() string {
		var state string
		if err := s.pool.QueryRow(t.Context(), `SELECT row(credential_hash, credential_revision, revoked_at IS NULL, count(*) OVER ())::text FROM devices WHERE id = $1`, runtime).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	if err := sessionAdapter(s).RegisterAgentHost(t.Context(), config.AgentHostID, config.AgentHostCredentialHash); err != nil {
		t.Fatal(err)
	}
	registered := row()
	if want := "(" + runtimedevice.HashCredential(credential) + ",1,t,1)"; registered != want {
		t.Fatalf("registered %s, want %s", registered, want)
	}
	if _, err := attachLink(t, startLinkRoute(t, s), runtime, []byte(credential)); err != nil {
		t.Fatal("the Link refused the agent host", err)
	}
	if _, err := runtimegateway.NewAuthenticator(sessionAdapter(s)).AuthenticateBearer(t.Context(), runtime, credential); err != nil {
		t.Fatal("the Runtime gateway refused the agent host", err)
	}
	if err := sessionAdapter(s).RegisterAgentHost(t.Context(), config.AgentHostID, config.AgentHostCredentialHash); err != nil || row() != registered {
		t.Fatal("a second registration changed the agent host", err)
	}

	rotated := uuid.NewString()
	if err := sessionAdapter(s).RegisterAgentHost(t.Context(), runtime, runtimedevice.HashCredential(rotated)); err != nil {
		t.Fatal(err)
	}
	if want := "(" + runtimedevice.HashCredential(rotated) + ",2,t,1)"; row() != want {
		t.Fatal("rotation did not advance credential revision")
	}
	if _, err := runtimegateway.NewAuthenticator(sessionAdapter(s)).AuthenticateBearer(t.Context(), runtime, credential); !errors.Is(err, runtimegateway.ErrAuthBadCredential) {
		t.Fatal("rotation retained previous credential", err)
	}
}

// TestInitializationBindsAgentHost places a hosted and a self_hosted Session
// on the registered agent host and runs each Environment's initialization once
// its Link resource is Serving. The bind carries the resource and an attach
// grant. A bind that fails before any effect, here because the agent host's
// connection closes, leaves the initialization unclaimed, and a later pass
// completes it.
func TestInitializationBindsAgentHost(t *testing.T) {
	for _, environment := range []string{`{"type":"openai_hosted"}`, `{"type":"self_hosted","workspace_directory":"/workspace"}`} {
		t.Run(environment, func(t *testing.T) {
			// Hosted work is admitted only on a configured deployment.
			s, _ := configuredStore(t)
			tenant, host := uuid.NewString(), registerAgentHost(t, s)
			session, err := s.CreateSession(t.Context(), tenant, WithFixtureModelProvider(sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
				Configuration: json.RawMessage(`{"agent":{"model":"test-model"},"environment":` + environment + `}`),
				InitialFiles:  []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/input", Data: []byte("frozen")}}}))
			if err != nil {
				t.Fatal(err)
			}
			resource, serve := fixtureLinkResource(t, s, tenant, session)
			server := httptest.NewUnstartedServer(nil)
			endpoint := "ws://" + server.Listener.Addr().String() + "/api/v1/agent-daemon/ws"
			handler, registry, err := runtime.NewGateway(sessionAdapter(s), sessionService(t, s), runtimegateway.NewLinkAuthority(sessionAdapter(s)), endpoint)
			if err != nil {
				t.Fatal(err)
			}
			server.Config.Handler = handler
			server.Start()
			t.Cleanup(func() { server.Close(); runtime.CloseConnections(registry) })
			link := startLinkRoute(t, s)
			runWorker(t, startWorker(t, t.Context(), s, &execution.Dispatcher{Registry: registry, Links: link.Relay}))
			within(t, startLinkServe(t, link, serve, resource.Ref()).connected)

			dropped := &initializationPeer{apply: completedInitialization, binds: make(chan proto.AssignmentBindPayload, 1), closeOnBind: true, host: host}
			dropped.setRuntimeGateway(t, s, endpoint, registry, nil)
			if err := dropped.connect(sandbox.Bootstrap{}); err != nil {
				t.Fatal(err)
			}
			within(t, dropped.binds)
			awaitInitialization(t, s, tenant, session.Environment.ID, "pending")

			peer := &initializationPeer{apply: completedInitialization, binds: make(chan proto.AssignmentBindPayload, 1), host: host}
			peer.setRuntimeGateway(t, s, endpoint, registry, nil)
			if err := peer.connect(sandbox.Bootstrap{}); err != nil {
				t.Fatal(err)
			}
			if bind := within(t, peer.binds); bind.EnvironmentID != session.Environment.ID || bind.Resource == nil || *bind.Resource != resource || len(bind.AttachGrant) == 0 {
				t.Fatalf("agent host bind = %+v", bind)
			}
			awaitInitialization(t, s, tenant, session.Environment.ID, "complete")
			if bound, err := sessionAdapter(s).GetSessionDevice(t.Context(), tenant, session.ID); err != nil || bound.ID != host.ID {
				t.Fatal("Session placed on", bound.ID, err)
			}
		})
	}
}
