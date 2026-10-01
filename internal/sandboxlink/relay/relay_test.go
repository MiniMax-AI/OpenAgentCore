package relay_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

const wait = 5 * time.Second

var (
	sessionID    = sandboxwire.NewID()
	assignmentID = sandboxwire.NewID()
	tenantID     = sandboxwire.NewID()
	environment  = sandboxwire.NewID()
	resourceID   = sandboxwire.NewID()
)

func resource(generation uint64) sandboxlink.ResourceRef {
	return sandboxlink.ResourceRef{TenantID: tenantID, EnvironmentID: environment, Kind: sandboxlink.ResourceAllocation, ID: resourceID, Generation: generation}
}

func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(wait):
		t.Fatalf("timed out waiting for %T", *new(T))
		panic("unreachable")
	}
}

func put[T any](ch chan T, v T) {
	select {
	case ch <- v:
	default:
	}
}

// hooked runs a test's hook after the Authority decides a serve Hello or a
// renewal, so a test can hold the decision.
type hooked struct {
	*sandboxlinktest.Authority
	mu               sync.Mutex
	onServe, onRenew func()
}

func (h *hooked) hook(f *func()) {
	h.mu.Lock()
	hook := *f
	h.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (h *hooked) set(f *func(), hook func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	*f = hook
}

func (h *hooked) AuthenticateServe(ctx context.Context, hello sandboxlink.ServeHello) (sandboxlink.ServePeer, error) {
	peer, err := h.Authority.AuthenticateServe(ctx, hello)
	h.hook(&h.onServe)
	return peer, err
}

func (h *hooked) Renew(ctx context.Context, peer sandboxlink.AttachPeer, r sandboxlink.RenewAttachment) (sandboxlink.Authorization, error) {
	auth, err := h.Authority.Renew(ctx, peer, r)
	h.hook(&h.onRenew)
	return auth, err
}

type fixture struct {
	t       *testing.T
	auth    *hooked
	srv     *sandboxlinktest.Server
	runtime sandboxwire.ID
	link    *sandboxlink.AttachLink
	closed  chan sandboxlink.AttachmentClosed
	lease   time.Duration
}

func newFixture(t *testing.T) *fixture {
	auth := &hooked{Authority: sandboxlinktest.NewAuthority()}
	f := &fixture{t: t, auth: auth, srv: sandboxlinktest.StartRelay(t, relay.Config{Authority: auth}),
		runtime: sandboxwire.NewID(), closed: make(chan sandboxlink.AttachmentClosed, 16), lease: time.Minute}
	auth.AddRuntime([]byte("runtime credential"), f.runtime)
	link, err := sandboxlink.DialAttach(context.Background(), sandboxlink.AttachConfig{URL: f.srv.URL, TLS: f.srv.TLS,
		RuntimeID: f.runtime, Credential: []byte("runtime credential"),
		OnAttachmentClosed: func(c sandboxlink.AttachmentClosed) { put(f.closed, c) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { link.Close() })
	f.link = link
	return f
}

// servePeer is a serve peer whose File handler echoes until EOF and whose
// Process handler reports its bind sequence after one byte and then resets its
// stream.
type servePeer struct {
	instance  sandboxwire.ID
	connected chan sandboxlink.HelloAccepted
	conns     chan net.Conn
	lost      chan sandboxwire.ID
	restored  chan sandboxwire.ID
	closed    chan sandboxlink.CloseReason
	binds     chan sandboxlink.Bind
	echoed    chan error
	seqs      chan uint64
	done      chan struct{}
	err       error // Serve's result, once done is closed
}

func (p *servePeer) wait(t *testing.T) error {
	t.Helper()
	recv(t, p.done)
	return p.err
}

// serve starts a serve peer of generation and waits for its Hello to be
// accepted.
func (f *fixture) serve(generation uint64) *servePeer {
	p := f.startServe(generation)
	recv(f.t, p.connected)
	return p
}

func (f *fixture) startServe(generation uint64) *servePeer {
	credential := []byte(fmt.Sprintf("serve credential %d", generation))
	f.auth.AddServe(credential, sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: resource(generation)})
	p := &servePeer{instance: sandboxwire.NewID(), connected: make(chan sandboxlink.HelloAccepted, 16), conns: make(chan net.Conn, 16),
		lost: make(chan sandboxwire.ID, 16), restored: make(chan sandboxwire.ID, 16), closed: make(chan sandboxlink.CloseReason, 128),
		binds: make(chan sandboxlink.Bind, 16), echoed: make(chan error, 16), seqs: make(chan uint64, 16), done: make(chan struct{})}
	echo := func(_ context.Context, b sandboxlink.Bind, _ uint64, s sandboxlink.Stream) {
		put(p.binds, b)
		_, err := io.Copy(s, s)
		if err == nil {
			err = s.CloseWrite()
		}
		put(p.echoed, err)
	}
	resetAfterOne := func(_ context.Context, _ sandboxlink.Bind, seq uint64, s sandboxlink.Stream) {
		s.Read(make([]byte, 1))
		put(p.seqs, seq)
		s.Reset()
	}
	cfg := sandboxlink.ServeConfig{
		Dial: func(ctx context.Context) (net.Conn, error) {
			c, err := sandboxlink.DialWebSocket(ctx, f.srv.URL, f.srv.TLS)
			if err == nil {
				put(p.conns, c)
			}
			return c, err
		},
		Credential: credential, Resource: resource(generation), ServerInstanceID: p.instance,
		Services: []sandboxlink.ServiceHandler{
			{Service: sandboxlink.ServiceFile, Version: 1, Serve: echo},
			{Service: sandboxlink.ServiceProcess, Version: 1, Serve: resetAfterOne},
		},
		OnConnected:          func(a sandboxlink.HelloAccepted) { put(p.connected, a) },
		OnAttachmentLost:     func(id sandboxwire.ID) { put(p.lost, id) },
		OnAttachmentRestored: func(id sandboxwire.ID) { put(p.restored, id) },
		OnAttachmentClosed:   func(_ sandboxwire.ID, r sandboxlink.CloseReason) { put(p.closed, r) },
		MinBackoff:           10 * time.Millisecond,
		MaxBackoff:           50 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		p.err = sandboxlink.Serve(ctx, cfg)
		close(p.done)
	}()
	f.t.Cleanup(func() {
		cancel()
		<-p.done
	})
	return p
}

// grant authorizes the fixture's Runtime for the resource's generation.
func (f *fixture) grant(generation uint64) []byte {
	grant := []byte(fmt.Sprintf("grant %d", generation))
	f.auth.AddGrant(grant, sandboxlinktest.Grant{RuntimeID: f.runtime, Resource: resource(generation), SessionID: sessionID,
		AssignmentID: assignmentID, AssignmentEpoch: 1, Services: []sandboxlink.Service{sandboxlink.ServiceFile, sandboxlink.ServiceProcess}, Lease: f.lease})
	return grant
}

func (f *fixture) open(service sandboxlink.Service, attachment sandboxwire.ID, generation uint64, grant []byte) (sandboxlink.Stream, sandboxlink.Opened, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	return f.link.OpenService(ctx, sandboxlink.Open{Service: service, Version: 1, Resource: resource(generation), AttachmentID: attachment,
		SessionID: sessionID, AssignmentID: assignmentID, AssignmentEpoch: 1, AttachGrant: grant})
}

func (f *fixture) mustOpen(service sandboxlink.Service, attachment sandboxwire.ID, generation uint64) (sandboxlink.Stream, sandboxlink.Opened) {
	f.t.Helper()
	s, opened, err := f.open(service, attachment, generation, f.grant(generation))
	if err != nil {
		f.t.Fatal(err)
	}
	return s, opened
}

// assertAborted checks that s ends with an error that is not an orderly EOF.
func assertAborted(t *testing.T, s sandboxlink.Stream) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, s)
		done <- err
	}()
	if err := recv(t, done); err == nil {
		t.Fatal("stream ended with EOF, want an abort")
	}
}

func TestSpliceKeepsOrderAndAborts(t *testing.T) {
	f := newFixture(t)
	p := f.serve(1)

	s, opened := f.mustOpen(sandboxlink.ServiceFile, sandboxwire.NewID(), 1)
	if opened.ServerInstanceID != p.instance {
		t.Fatalf("server instance %s, want %s", opened.ServerInstanceID, p.instance)
	}
	if b := recv(t, p.binds); !reflect.DeepEqual(b.Exports, sandboxlinktest.DefaultExports) {
		t.Fatalf("file binding exports %+v, want %+v", b.Exports, sandboxlinktest.DefaultExports)
	}
	sent := make([]byte, 1<<20)
	rand.Read(sent)
	go func() {
		s.Write(sent)
		s.CloseWrite()
	}()
	got, err := io.ReadAll(s)
	if err != nil || !bytes.Equal(got, sent) {
		t.Fatalf("echo returned %d bytes, %v; want the %d bytes sent and EOF", len(got), err, len(sent))
	}
	if err := recv(t, p.echoed); err != nil {
		t.Fatalf("serve side: %v, want EOF", err)
	}

	s, _ = f.mustOpen(sandboxlink.ServiceFile, sandboxwire.NewID(), 1)
	s.Write([]byte("x"))
	s.Reset()
	if err := recv(t, p.echoed); err == nil {
		t.Fatal("attach reset reached the serve peer as EOF")
	}

	s, _ = f.mustOpen(sandboxlink.ServiceProcess, sandboxwire.NewID(), 1)
	s.Write([]byte("x"))
	assertAborted(t, s)
}

func TestUnauthorizedOpen(t *testing.T) {
	f := newFixture(t)
	f.serve(1)
	if _, _, err := f.open(sandboxlink.ServiceFile, sandboxwire.NewID(), 1, []byte("forged grant")); !errors.Is(err, sandboxlink.PermissionDenied) {
		t.Fatalf("open with an unknown grant: %v, want PermissionDenied", err)
	}
}

func TestGenerations(t *testing.T) {
	f := newFixture(t)
	old := f.serve(1)
	attachment := sandboxwire.NewID()
	s, _ := f.mustOpen(sandboxlink.ServiceFile, attachment, 1)

	newer := f.serve(2)
	if c := recv(t, f.closed); c.AttachmentID != attachment || c.Reason != sandboxlink.CloseStaleGeneration {
		t.Fatalf("closed %+v, want %s closed as stale", c, attachment)
	}
	assertAborted(t, s)
	if err := old.wait(t); !errors.Is(err, sandboxlink.StaleGeneration) {
		t.Fatalf("older serve peer reconnect: %v, want StaleGeneration", err)
	}
	if _, _, err := f.open(sandboxlink.ServiceFile, sandboxwire.NewID(), 1, f.grant(1)); !errors.Is(err, sandboxlink.StaleGeneration) {
		t.Fatalf("open of generation 1: %v, want StaleGeneration", err)
	}
	if _, opened := f.mustOpen(sandboxlink.ServiceFile, sandboxwire.NewID(), 2); opened.ServerInstanceID != newer.instance {
		t.Fatalf("generation 2 served by %s, want %s", opened.ServerInstanceID, newer.instance)
	}
}

func TestLeaseExpiry(t *testing.T) {
	f := newFixture(t)
	p := f.serve(1)
	f.lease = 300 * time.Millisecond
	attachment := sandboxwire.NewID()
	s, opened := f.mustOpen(sandboxlink.ServiceFile, attachment, 1)

	time.Sleep(20 * time.Millisecond) // leases have millisecond resolution
	renewed, err := f.link.Renew(context.Background(), sandboxlink.RenewAttachment{AttachmentID: attachment, AttachGrant: f.grant(1)})
	if err != nil || !renewed.LeaseExpiresAt.After(opened.LeaseExpiresAt) {
		t.Fatalf("renew: %+v %v, want a lease after %s", renewed, err, opened.LeaseExpiresAt)
	}
	if c := recv(t, f.closed); c.AttachmentID != attachment || c.Reason != sandboxlink.CloseLeaseExpired {
		t.Fatalf("closed %+v, want %s closed by lease expiry", c, attachment)
	}
	assertAborted(t, s)
	if r := recv(t, p.closed); r != sandboxlink.CloseLeaseExpired {
		t.Fatalf("serve peer saw close reason %d, want lease expiry", r)
	}
}

func TestRevoke(t *testing.T) {
	f := newFixture(t)
	p := f.serve(1)
	first, second := sandboxwire.NewID(), sandboxwire.NewID()
	s1, _ := f.mustOpen(sandboxlink.ServiceFile, first, 1)
	s2, _ := f.mustOpen(sandboxlink.ServiceFile, second, 1)

	f.srv.Relay.RevokeAttachment(first)
	if c := recv(t, f.closed); c.AttachmentID != first || c.Reason != sandboxlink.CloseRevoked {
		t.Fatalf("closed %+v, want %s revoked", c, first)
	}
	assertAborted(t, s1)
	s2.Write([]byte("still open"))
	s2.CloseWrite()
	if got, err := io.ReadAll(s2); err != nil || string(got) != "still open" {
		t.Fatalf("other attachment read %q, %v", got, err)
	}

	s3, _ := f.mustOpen(sandboxlink.ServiceFile, second, 1)
	f.auth.RemoveServe([]byte("serve credential 1"))
	f.srv.Relay.RevokeResource(resource(1))
	if c := recv(t, f.closed); c.AttachmentID != second || c.Reason != sandboxlink.CloseRevoked {
		t.Fatalf("closed %+v, want %s revoked", c, second)
	}
	assertAborted(t, s3)
	if err := p.wait(t); !errors.Is(err, sandboxlink.AuthenticationFailed) {
		t.Fatalf("revoked serve peer reconnect: %v, want AuthenticationFailed", err)
	}
}

// A serve Hello decided before a revocation must not install the revoked peer.
func TestRevokeDuringServeHello(t *testing.T) {
	f := newFixture(t)
	decided, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.auth.set(&f.auth.onServe, func() {
		once.Do(func() { close(decided) })
		<-release
	})
	p := f.startServe(1)
	recv(t, decided)
	f.auth.RemoveServe([]byte("serve credential 1"))
	f.srv.Relay.RevokeResource(resource(1))
	close(release)
	if err := p.wait(t); !errors.Is(err, sandboxlink.AuthenticationFailed) {
		t.Fatalf("serve peer revoked during its Hello: %v, want AuthenticationFailed", err)
	}
	if len(p.connected) != 0 {
		t.Fatal("revoked serve peer was accepted")
	}
}

// Renewals beyond MaxControlRequests are refused before reaching the Authority.
func TestRenewalsAreBounded(t *testing.T) {
	f := newFixture(t)
	f.serve(1)
	attachment := sandboxwire.NewID()
	f.mustOpen(sandboxlink.ServiceFile, attachment, 1)
	grant := f.grant(1)
	const extra = 4
	entered, release := make(chan struct{}, 2*sandboxlink.MaxControlRequests), make(chan struct{})
	f.auth.set(&f.auth.onRenew, func() {
		entered <- struct{}{}
		<-release
	})
	errs := make(chan error, sandboxlink.MaxControlRequests+extra)
	for range sandboxlink.MaxControlRequests + extra {
		go func() {
			_, err := f.link.Renew(context.Background(), sandboxlink.RenewAttachment{AttachmentID: attachment, AttachGrant: grant})
			errs <- err
		}()
	}
	for range extra {
		if err := recv(t, errs); !errors.Is(err, sandboxlink.LimitExceeded) {
			t.Fatalf("renewal beyond the bound: %v, want LimitExceeded", err)
		}
	}
	for range sandboxlink.MaxControlRequests {
		recv(t, entered)
	}
	close(release)
	for range sandboxlink.MaxControlRequests {
		if err := recv(t, errs); err != nil {
			t.Fatalf("renewal within the bound: %v", err)
		}
	}
	if len(entered) != 0 {
		t.Fatalf("%d renewals beyond the bound reached the Authority", len(entered))
	}
}

// AttachmentClosed events for a serve peer that is away all reach it when it
// reconnects, however many there are.
func TestClosuresReplayOnReconnect(t *testing.T) {
	f := newFixture(t)
	p := f.serve(1)
	ids := make([]sandboxwire.ID, 100)
	for i := range ids {
		ids[i] = sandboxwire.NewID()
		f.mustOpen(sandboxlink.ServiceFile, ids[i], 1)
	}
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.auth.set(&f.auth.onServe, func() {
		once.Do(func() { close(held) })
		<-release
	})
	recv(t, p.conns).Close()
	recv(t, held)
	for _, id := range ids {
		f.srv.Relay.RevokeAttachment(id)
	}
	close(release)
	for range ids {
		if r := recv(t, p.closed); r != sandboxlink.CloseRevoked {
			t.Fatalf("serve peer saw close reason %d, want revocation", r)
		}
	}
}

// A control call whose context ended before it was sent fails with no effect
// and leaves the link up.
func TestCancelledCallKeepsLink(t *testing.T) {
	f := newFixture(t)
	f.serve(1)
	attachment := sandboxwire.NewID()
	f.mustOpen(sandboxlink.ServiceFile, attachment, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 20 {
		err := f.link.CloseAttachment(ctx, attachment)
		var e *sandboxlink.Error
		if !errors.As(err, &e) || e.Effect != sandboxwire.EffectNone || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled close: %v, want EffectNone caused by the cancellation", err)
		}
	}
	if _, err := f.link.Renew(context.Background(), sandboxlink.RenewAttachment{AttachmentID: attachment, AttachGrant: f.grant(1)}); err != nil {
		t.Fatalf("renew after cancelled calls: %v", err)
	}
}

func TestServeReconnect(t *testing.T) {
	f := newFixture(t)
	p := f.serve(1)
	attachment := sandboxwire.NewID()
	s, opened := f.mustOpen(sandboxlink.ServiceFile, attachment, 1)

	recv(t, p.conns).Close()
	if id := recv(t, p.lost); id != attachment {
		t.Fatalf("lost %s, want %s", id, attachment)
	}
	assertAborted(t, s)
	recv(t, p.connected)

	grant := f.grant(1)
	s, err := func() (sandboxlink.Stream, error) {
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()
		s, _, err := f.link.OpenService(ctx, sandboxlink.Open{Service: sandboxlink.ServiceFile, Version: 1, Resource: resource(1),
			ExpectedServerInstanceID: opened.ServerInstanceID, AttachmentID: attachment,
			SessionID: sessionID, AssignmentID: assignmentID, AssignmentEpoch: 1, AttachGrant: grant})
		return s, err
	}()
	if err != nil {
		t.Fatalf("reopen after reconnect: %v", err)
	}
	defer s.Close()
	if id := recv(t, p.restored); id != attachment {
		t.Fatalf("restored %s, want %s", id, attachment)
	}
}

// Handlers receive bind sequences in bind order however late they run, and the
// sequence keeps increasing after the serve peer reconnects.
func TestBindSequence(t *testing.T) {
	f := newFixture(t)
	p := f.serve(1)
	attachment := sandboxwire.NewID()
	// A Process handler reports its sequence once its stream's first byte
	// arrives, so the test decides which handler runs first.
	seq := func(s sandboxlink.Stream) uint64 {
		t.Helper()
		if _, err := s.Write([]byte{0}); err != nil {
			t.Fatal(err)
		}
		return recv(t, p.seqs)
	}
	first, _ := f.mustOpen(sandboxlink.ServiceProcess, attachment, 1)
	second, _ := f.mustOpen(sandboxlink.ServiceProcess, attachment, 1)
	later := seq(second)
	earlier := seq(first)
	recv(t, p.conns).Close()
	recv(t, p.connected)
	third, _ := f.mustOpen(sandboxlink.ServiceProcess, attachment, 1)
	reconnected := seq(third)
	if !(0 < earlier && earlier < later && later < reconnected) {
		t.Fatalf("bind sequences %d, %d, then %d after a reconnect; want them to increase from above zero", earlier, later, reconnected)
	}
}
