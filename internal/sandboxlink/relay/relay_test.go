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

// hooked runs a test's hook after the Authority decides a serve Hello, an
// Open or a renewal, so a test can hold the decision.
type hooked struct {
	*sandboxlinktest.Authority
	mu                       sync.Mutex
	onServe, onOpen, onRenew func()
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

func (h *hooked) AuthorizeOpen(ctx context.Context, peer sandboxlink.AttachPeer, o sandboxlink.Open) (sandboxlink.Authorization, error) {
	auth, err := h.Authority.AuthorizeOpen(ctx, peer, o)
	h.hook(&h.onOpen)
	return auth, err
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
	f := &fixture{t: t, auth: auth, srv: sandboxlinktest.StartRelay(t, auth),
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
	connected chan struct{}
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
	p := &servePeer{instance: sandboxwire.NewID(), connected: make(chan struct{}, 16), conns: make(chan net.Conn, 16),
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
		OnConnected:          func() { put(p.connected, struct{}{}) },
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

// serveHello sends a serve Hello for ref with an unknown credential over a new
// link and returns the relay's answer.
func (f *fixture) serveHello(ref sandboxlink.ResourceRef) error {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	conn, err := sandboxlink.DialWebSocket(ctx, f.srv.URL, f.srv.TLS)
	if err != nil {
		return err
	}
	sess, _ := sandboxlink.ClientSession(conn)
	defer sess.Close()
	ctl, err := sess.OpenStream(ctx)
	if err == nil {
		ctl.SetDeadline(time.Now().Add(wait))
		err = sandboxlink.WriteMessage(ctl, 1, sandboxlink.ServeHello{Version: sandboxlink.Version, Credential: []byte("unknown"), Resource: ref,
			ServerInstanceID: sandboxwire.NewID(), Services: []sandboxlink.ServiceVersion{{Service: sandboxlink.ServiceFile, Version: 1}}})
	}
	if err == nil {
		_, err = sandboxlink.ReadReply(ctl, sandboxlink.OpHello, 1)
	}
	return err
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

// A resource is served at the generation of its current serve peer only, and
// no longer once the relay revokes it.
func TestServing(t *testing.T) {
	f := newFixture(t)
	if f.srv.Relay.Serving(resource(1)) {
		t.Fatal("serving before any serve peer")
	}
	f.serve(1)
	if !f.srv.Relay.Serving(resource(1)) || f.srv.Relay.Serving(resource(2)) {
		t.Fatal("generation 1's serve peer does not serve generation 1 alone")
	}
	f.serve(2)
	if f.srv.Relay.Serving(resource(1)) || !f.srv.Relay.Serving(resource(2)) {
		t.Fatal("generation 2's serve peer does not replace generation 1")
	}
	f.auth.RemoveServe([]byte("serve credential 2"))
	f.srv.Relay.RevokeResource(resource(2))
	if f.srv.Relay.Serving(resource(2)) {
		t.Fatal("serving after revocation")
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

// A serve Hello for a resource the relay does not hold takes a slot before the
// Authority decides: beyond capacity even an unknown credential gets
// LimitExceeded. A refused Hello leaves no slot taken, and an admitted
// resource still reconnects at capacity.
func TestServesAreBounded(t *testing.T) {
	f := newFixture(t)
	relay.SetLimits(f.srv.Relay, 1, 16, 16)
	other := resource(1)
	other.ID = sandboxwire.NewID()
	if err := f.serveHello(other); !errors.Is(err, sandboxlink.AuthenticationFailed) {
		t.Fatalf("serve Hello with an unknown credential: %v, want AuthenticationFailed", err)
	}
	p := f.serve(1)
	if err := f.serveHello(other); !errors.Is(err, sandboxlink.LimitExceeded) {
		t.Fatalf("serve Hello beyond capacity: %v, want LimitExceeded", err)
	}
	if resources, _, _ := relay.Held(f.srv.Relay); resources != 1 {
		t.Fatalf("relay holds %d resources, want 1", resources)
	}
	recv(t, p.conns).Close()
	recv(t, p.connected)
}

// Each Hello for a resource takes one of its MaxServeHellos while it is
// decided: past them, a Hello for a held resource gets LimitExceeded without
// reaching the Authority.
func TestServeHellosAreBounded(t *testing.T) {
	f := newFixture(t)
	f.serve(1)
	entered, release := make(chan struct{}, relay.MaxServeHellos+1), make(chan struct{})
	f.auth.set(&f.auth.onServe, func() {
		entered <- struct{}{}
		<-release
	})
	errs := make(chan error, relay.MaxServeHellos)
	for range relay.MaxServeHellos {
		go func() { errs <- f.serveHello(resource(1)) }()
		recv(t, entered)
	}
	if err := f.serveHello(resource(1)); !errors.Is(err, sandboxlink.LimitExceeded) {
		t.Fatalf("serve Hello beyond the bound: %v, want LimitExceeded", err)
	}
	close(release)
	for range relay.MaxServeHellos {
		if err := recv(t, errs); !errors.Is(err, sandboxlink.AuthenticationFailed) {
			t.Fatalf("serve Hello within the bound: %v, want AuthenticationFailed", err)
		}
	}
	if len(entered) != 0 {
		t.Fatal("a serve Hello beyond the bound reached the Authority")
	}
}

// An Open takes one of its link's MaxStreams before the Authority decides and
// keeps it until the decision ends, even once the peer has reset the stream.
func TestOpenDecisionsAreBounded(t *testing.T) {
	f := newFixture(t)
	f.serve(1)
	attachment := sandboxwire.NewID()
	f.mustOpen(sandboxlink.ServiceFile, attachment, 1) // its stream takes one
	grant := f.grant(1)
	pending := relay.MaxStreams - 1
	entered, release := make(chan struct{}, pending+1), make(chan struct{})
	f.auth.set(&f.auth.onOpen, func() {
		entered <- struct{}{}
		<-release
	})
	reset := make(chan error)
	for range pending {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			_, _, err := f.link.OpenService(ctx, sandboxlink.Open{Service: sandboxlink.ServiceFile, Version: 1, Resource: resource(1),
				AttachmentID: attachment, SessionID: sessionID, AssignmentID: assignmentID, AssignmentEpoch: 1, AttachGrant: grant})
			reset <- err
		}()
		recv(t, entered)
		cancel() // OpenService resets the stream
		recv(t, reset)
	}
	if _, _, err := f.open(sandboxlink.ServiceFile, attachment, 1, grant); !errors.Is(err, sandboxlink.LimitExceeded) {
		t.Fatalf("open beyond the stream bound: %v, want LimitExceeded", err)
	}
	if len(entered) != 0 {
		t.Fatal("an Open beyond the bound reached the Authority")
	}
	f.auth.set(&f.auth.onOpen, nil)
	close(release)
	// The slots return as the decisions end; until then an Open is refused
	// with LimitExceeded, which is retryable.
	for {
		_, _, err := f.open(sandboxlink.ServiceFile, attachment, 1, grant)
		if err == nil {
			break
		}
		if !errors.Is(err, sandboxlink.LimitExceeded) {
			t.Fatalf("open after the decisions ended: %v", err)
		}
	}
}

// An Open that finds an attachment fails with LeaseExpired when that
// attachment closes while the Open is decided, even if another Open has
// created a new attachment under the same ID since, and leaves the new
// attachment's lease alone.
func TestOpenOfAReplacedAttachment(t *testing.T) {
	f := newFixture(t)
	p := f.serve(1)
	calls := make(chan chan struct{})
	f.auth.set(&f.auth.onOpen, func() {
		release := make(chan struct{})
		calls <- release
		<-release
	})
	attachment := sandboxwire.NewID()
	open := func(grant []byte) <-chan error {
		errs := make(chan error, 1)
		go func() {
			_, _, err := f.open(sandboxlink.ServiceFile, attachment, 1, grant)
			errs <- err
		}()
		return errs
	}
	grant := f.grant(1)
	longer := []byte("longer grant")
	f.auth.AddGrant(longer, sandboxlinktest.Grant{RuntimeID: f.runtime, Resource: resource(1), SessionID: sessionID,
		AssignmentID: assignmentID, AssignmentEpoch: 1, Services: []sandboxlink.Service{sandboxlink.ServiceFile}, Lease: 2 * f.lease})

	// Two Opens arrive while the relay holds no attachment; the first creates A.
	first := open(grant)
	releaseFirst := recv(t, calls)
	second := open(grant)
	releaseSecond := recv(t, calls)
	close(releaseFirst)
	if err := recv(t, first); err != nil {
		t.Fatal(err)
	}
	// A third Open finds A, which closes while it is decided.
	third := open(longer)
	releaseThird := recv(t, calls)
	if err := f.link.CloseAttachment(context.Background(), attachment); err != nil {
		t.Fatal(err)
	}
	recv(t, p.closed)
	// The second Open creates B under the same ID. The serve peer, which saw
	// A close, refuses to bind it, but the relay holds B.
	close(releaseSecond)
	recv(t, second)
	lease := relay.Lease(f.srv.Relay, attachment)
	if lease.IsZero() {
		t.Fatal("the second Open created no attachment")
	}
	close(releaseThird)
	if err := recv(t, third); !errors.Is(err, sandboxlink.LeaseExpired) {
		t.Fatalf("open of a replaced attachment: %v, want LeaseExpired", err)
	}
	if got := relay.Lease(f.srv.Relay, attachment); !got.Equal(lease) {
		t.Fatalf("the new attachment's lease moved from %s to %s", lease, got)
	}
}

// An Open of an attachment the relay does not hold takes a slot before the
// Authority decides: beyond capacity even a forged grant gets LimitExceeded.
// A refused Open leaves no slot taken, and an open attachment still opens
// streams at capacity.
func TestAttachmentsAreBounded(t *testing.T) {
	f := newFixture(t)
	relay.SetLimits(f.srv.Relay, 16, 1, 16)
	f.serve(1)
	if _, _, err := f.open(sandboxlink.ServiceFile, sandboxwire.NewID(), 1, []byte("forged grant")); !errors.Is(err, sandboxlink.PermissionDenied) {
		t.Fatalf("open with a forged grant: %v, want PermissionDenied", err)
	}
	attachment := sandboxwire.NewID()
	f.mustOpen(sandboxlink.ServiceFile, attachment, 1)
	if _, _, err := f.open(sandboxlink.ServiceFile, sandboxwire.NewID(), 1, []byte("forged grant")); !errors.Is(err, sandboxlink.LimitExceeded) {
		t.Fatalf("open of a new attachment beyond capacity: %v, want LimitExceeded", err)
	}
	f.mustOpen(sandboxlink.ServiceFile, attachment, 1)
	if _, attachments, _ := relay.Held(f.srv.Relay); attachments != 1 {
		t.Fatalf("relay holds %d attachment slots, want 1", attachments)
	}
}

// A closed attachment keeps its slot until its AttachmentClosed event is
// written to the serve peer, which is away and then reconnects.
func TestClosuresKeepTheirSlots(t *testing.T) {
	f := newFixture(t)
	relay.SetLimits(f.srv.Relay, 16, 2, 16)
	p := f.serve(1)
	ids := []sandboxwire.ID{sandboxwire.NewID(), sandboxwire.NewID()}
	for _, id := range ids {
		f.mustOpen(sandboxlink.ServiceFile, id, 1)
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
		if err := f.link.CloseAttachment(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := f.open(sandboxlink.ServiceFile, sandboxwire.NewID(), 1, f.grant(1)); !errors.Is(err, sandboxlink.LimitExceeded) {
		t.Fatalf("open while both closures are unwritten: %v, want LimitExceeded", err)
	}
	close(release)
	for range ids {
		if r := recv(t, p.closed); r != sandboxlink.CloseRequested {
			t.Fatalf("serve peer saw close reason %d, want a requested close", r)
		}
	}
	// The relay frees an event's slot before it writes the next, so one slot
	// is free once both events have arrived.
	f.mustOpen(sandboxlink.ServiceFile, sandboxwire.NewID(), 1)
}

// An attach Hello takes a link slot before the Authority decides: beyond
// capacity even an unknown credential gets LimitExceeded.
func TestAttachLinksAreBounded(t *testing.T) {
	f := newFixture(t)
	relay.SetLimits(f.srv.Relay, 16, 16, 1)
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	_, err := sandboxlink.DialAttach(ctx, sandboxlink.AttachConfig{URL: f.srv.URL, TLS: f.srv.TLS, RuntimeID: sandboxwire.NewID(), Credential: []byte("unknown")})
	if !errors.Is(err, sandboxlink.LimitExceeded) {
		t.Fatalf("attach Hello beyond capacity: %v, want LimitExceeded", err)
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

// A request fails with a final ProtocolViolation and EffectPossible when its
// answer carries another request ID, request ID zero or another operation. The
// relay passes a Bind's failure on to the Open. A CloseAttachment answer under
// another request ID is left out: the link drops it as a late answer.
func TestAnswersMatchTheirRequests(t *testing.T) {
	for _, op := range []sandboxlink.Op{sandboxlink.OpHello, sandboxlink.OpOpen, sandboxlink.OpBind, sandboxlink.OpCloseAttachment} {
		for _, kind := range []string{"wrong ID", "zero ID", "wrong op"} {
			if op == sandboxlink.OpCloseAttachment && kind == "wrong ID" {
				continue
			}
			t.Run(fmt.Sprintf("op %d %s", op, kind), func(t *testing.T) {
				// answer writes m as the answer to request id, spoiled as kind
				// says. A fake that fails leaves its caller without a violation.
				answer := func(w io.Writer, id uint64, m sandboxlink.Message) {
					switch kind {
					case "wrong ID":
						id++
					case "zero ID":
						id = 0
					default:
						m = sandboxlink.FailureFor(sandboxlink.OpRenewAttachment, sandboxlink.Fail(sandboxlink.ServiceUnavailable))
					}
					fr, _ := sandboxlink.Encode(1, m)
					fr.RequestID = id
					sandboxwire.WriteFrame(w, fr)
				}
				ctx, cancel := context.WithTimeout(context.Background(), wait)
				defer cancel()
				f, grant := &fixture{t: t}, []byte("grant")
				var err error
				if op == sandboxlink.OpBind {
					f = newFixture(t)
					grant = f.grant(1)
					credential := []byte("serve credential")
					f.auth.AddServe(credential, sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: resource(1)})
					conn, err := sandboxlink.DialWebSocket(ctx, f.srv.URL, f.srv.TLS)
					if err != nil {
						t.Fatal(err)
					}
					sess, _ := sandboxlink.ClientSession(conn)
					t.Cleanup(func() { sess.Close() })
					ctl, err := sess.OpenStream(ctx)
					if err == nil {
						err = sandboxlink.WriteMessage(ctl, 1, sandboxlink.ServeHello{Version: sandboxlink.Version, Credential: credential, Resource: resource(1),
							ServerInstanceID: sandboxwire.NewID(), Services: []sandboxlink.ServiceVersion{{Service: sandboxlink.ServiceFile, Version: 1}}})
					}
					if err == nil {
						_, err = sandboxlink.ReadReply(ctl, sandboxlink.OpHello, 1)
					}
					if err != nil {
						t.Fatal(err)
					}
					go func() {
						st, err := sess.AcceptStream()
						if err != nil {
							return
						}
						id, _, _ := sandboxlink.ReadMessage(st)
						answer(st, id, sandboxlink.Bound{})
					}()
				} else {
					client, server := net.Pipe()
					t.Cleanup(func() { server.Close() })
					go func() {
						sess, _ := sandboxlink.ServerSession(server)
						ctl, err := sess.AcceptStream()
						if err != nil {
							return
						}
						id, _, _ := sandboxlink.ReadMessage(ctl)
						if op == sandboxlink.OpHello {
							answer(ctl, id, sandboxlink.HelloAccepted{})
							return
						}
						sandboxlink.WriteMessage(ctl, id, sandboxlink.HelloAccepted{})
						if op == sandboxlink.OpCloseAttachment {
							id, _, _ = sandboxlink.ReadMessage(ctl)
							answer(ctl, id, sandboxlink.CloseAccepted{})
							return
						}
						st, err := sess.AcceptStream()
						if err != nil {
							return
						}
						id, m, _ := sandboxlink.ReadMessage(st)
						o, _ := m.(sandboxlink.Open)
						answer(st, id, sandboxlink.Opened{AttachmentID: o.AttachmentID, ServerInstanceID: sandboxwire.NewID(), LeaseExpiresAt: time.Now().Add(time.Minute)})
					}()
					f.link, err = sandboxlink.DialAttach(ctx, sandboxlink.AttachConfig{RuntimeID: sandboxwire.NewID(), Credential: []byte("runtime credential"),
						Dial: func(context.Context) (net.Conn, error) { return client, nil }})
				}
				switch {
				case op == sandboxlink.OpHello:
				case err != nil:
					t.Fatal(err)
				case op == sandboxlink.OpCloseAttachment:
					err = f.link.CloseAttachment(ctx, sandboxwire.NewID())
				default:
					_, _, err = f.open(sandboxlink.ServiceFile, sandboxwire.NewID(), 1, grant)
				}
				var e *sandboxlink.Error
				if !errors.As(err, &e) || e.Code != sandboxlink.ProtocolViolation || e.Effect != sandboxwire.EffectPossible || e.Code.Retryable() {
					t.Fatalf("%v, want a ProtocolViolation with EffectPossible", err)
				}
			})
		}
	}
}
