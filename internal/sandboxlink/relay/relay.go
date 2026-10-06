// Package relay is the Link relay core. It authenticates serve and attach
// peers through a sandboxlink.Authority, authorizes every opened service
// stream, binds it to the current serve peer of its resource and splices the
// two streams without reading them. Core embeds it; tests run it through
// sandboxlinktest. docs/sandbox-link-protocol.md describes the protocol.
package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/libp2p/go-yamux/v5"
)

const (
	// maxStreams bounds the concurrent service streams of each link.
	maxStreams = 256
	// spliceBuffer bounds the bytes a splice holds per direction, on top of
	// one yamux window per stream.
	spliceBuffer = 32 << 10
	// authorizeAttempts bounds how often a serve Hello or an Open is decided
	// again when a revocation races the Authority's decision.
	authorizeAttempts = 3
	// answerQueue bounds the control answers queued for one peer.
	answerQueue = 64
)

// Relay accepts Link peers on ServeHTTP. It keeps the current serve peer of
// each resource, the attachments it has opened and their leases in memory; the
// Authority stays the durable judge of every grant.
type Relay struct {
	auth   sandboxlink.Authority
	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	epoch       uint64 // counts revocations, so a racing Open re-authorizes
	generations map[resourceKey]uint64
	serves      map[resourceKey]*serveLink
	// closures holds the AttachmentClosed events not yet written to the serve
	// peer of each resource's newest generation, connected or not.
	closures    map[resourceKey]closures
	attachments map[sandboxwire.ID]*attachment
}

// closures is a set of AttachmentClosed events to write, by attachment.
type closures map[sandboxwire.ID]sandboxlink.CloseReason

// New returns a relay that asks auth to authenticate peers and authorize their
// requests.
func New(auth sandboxlink.Authority) *Relay {
	ctx, cancel := context.WithCancel(context.Background())
	return &Relay{auth: auth, ctx: ctx, cancel: cancel,
		generations: map[resourceKey]uint64{},
		serves:      map[resourceKey]*serveLink{},
		closures:    map[resourceKey]closures{},
		attachments: map[sandboxwire.ID]*attachment{},
	}
}

// Close ends every link and stops lease timers.
func (rl *Relay) Close() error {
	rl.cancel()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for _, a := range rl.attachments {
		a.timer.Stop()
	}
	return nil
}

// RevokeAttachment closes an attachment and its streams. The caller withdraws
// the authority first, so a racing Open re-authorizes and fails.
func (rl *Relay) RevokeAttachment(id sandboxwire.ID) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.epoch++
	if a := rl.attachments[id]; a != nil {
		rl.closeLocked(a, sandboxlink.CloseRevoked)
	}
}

// RevokeResource closes every attachment of ref's generation and older and
// disconnects that serve peer after writing it their AttachmentClosed events.
// Events it could not write stay for a reconnect of the same generation. The
// caller withdraws the authority first.
func (rl *Relay) RevokeResource(ref sandboxlink.ResourceRef) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.epoch++
	for _, a := range rl.attachments {
		if r := a.identity.Resource; r.SameResource(ref) && r.Generation <= ref.Generation {
			rl.closeLocked(a, sandboxlink.CloseRevoked)
		}
	}
	key := keyOf(ref)
	if sl := rl.serves[key]; sl != nil && sl.hello.Resource.Generation <= ref.Generation {
		delete(rl.serves, key)
		sl.end()
	}
}

type resourceKey struct {
	tenant, environment, id sandboxwire.ID
	kind                    sandboxlink.ResourceKind
}

func keyOf(r sandboxlink.ResourceRef) resourceKey {
	return resourceKey{tenant: r.TenantID, environment: r.EnvironmentID, id: r.ID, kind: r.Kind}
}

// link is one authenticated connection. Its writer sends answers from a
// bounded queue and AttachmentClosed events from an unbounded set, so the
// relay never blocks on a peer while holding its lock and never drops an
// event: an event leaves the set only once it is written.
type link struct {
	sess *yamux.Session
	ctl  *yamux.Stream
	out  chan outgoing
	wake chan struct{} // the set has events to write
	// Under Relay.mu: the events to write and the open service streams. A
	// serve link shares its resource's set, which outlives the link.
	closures closures
	streams  uint32
}

type outgoing struct {
	id  uint64
	m   sandboxlink.Message // nil only to end the link
	end bool                // end the link after writing m
}

type serveLink struct {
	*link
	hello sandboxlink.ServeHello
}

type attachLink struct {
	*link
	peer sandboxlink.AttachPeer
	// inflight counts control requests being decided, at most
	// sandboxlink.MaxControlRequests.
	inflight atomic.Int32
}

func (rl *Relay) newLink(sess *yamux.Session, ctl *yamux.Stream) *link {
	l := &link{sess: sess, ctl: ctl, out: make(chan outgoing, answerQueue), wake: make(chan struct{}, 1)}
	go rl.write(l)
	return l
}

// write runs l's writer. Queued answers go first, so HelloAccepted precedes
// every event, and the events are written after each answer and before the
// link ends.
func (rl *Relay) write(l *link) {
	for {
		var o outgoing
		select {
		case o = <-l.out:
		default:
			select {
			case o = <-l.out:
			case <-l.wake:
			case <-l.sess.CloseChan():
				return
			}
		}
		var err error
		if o.m != nil {
			err = sandboxlink.WriteMessage(l.ctl, o.id, o.m)
		}
		if err == nil {
			err = rl.writeClosures(l)
		}
		if err != nil || o.end {
			if err == nil {
				l.ctl.CloseWrite()
				linger(l.sess)
			}
			l.sess.Close()
			return
		}
	}
}

// writeClosures writes l's pending AttachmentClosed events one at a time,
// removing each from the set once it is written.
func (rl *Relay) writeClosures(l *link) error {
	for {
		rl.mu.Lock()
		var c sandboxlink.AttachmentClosed
		for id, reason := range l.closures {
			c = sandboxlink.AttachmentClosed{AttachmentID: id, Reason: reason}
			break
		}
		rl.mu.Unlock()
		if c.Reason == 0 {
			return nil
		}
		if err := sandboxlink.WriteMessage(l.ctl, 0, c); err != nil {
			return err
		}
		rl.mu.Lock()
		if l.closures[c.AttachmentID] == c.Reason {
			delete(l.closures, c.AttachmentID)
		}
		rl.mu.Unlock()
	}
}

// closedLocked adds an AttachmentClosed event to l's set and wakes its writer.
func (l *link) closedLocked(id sandboxwire.ID, reason sandboxlink.CloseReason) {
	if l.closures == nil {
		l.closures = closures{}
	}
	l.closures[id] = reason
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// send queues a control answer. A peer that stops reading loses its link.
func (l *link) send(id uint64, m sandboxlink.Message) { l.queue(outgoing{id: id, m: m}) }

// end closes the link once every answer queued before it and every pending
// event is written.
func (l *link) end() { l.queue(outgoing{end: true}) }

// fail answers a request that ends the link.
func (l *link) fail(id uint64, op sandboxlink.Op, code sandboxlink.Code) {
	if !sandboxwire.ValidRequestID(id) {
		l.sess.Close()
		return
	}
	l.queue(outgoing{id: id, m: sandboxlink.FailureFor(op, sandboxlink.Fail(code)), end: true})
}

func (l *link) queue(o outgoing) {
	select {
	case l.out <- o:
	default:
		l.sess.Close()
	}
}

// linger gives the peer time to read a final answer before the link closes.
// yamux drops queued frames when a session closes, so the relay waits for the
// peer to hang up.
func linger(sess *yamux.Session) {
	select {
	case <-sess.CloseChan():
	case <-time.After(sandboxlink.HandshakeTimeout):
	}
}

// attachment is an attachment the relay has opened. It outlives the links
// that carry its streams until its lease expires or it is closed.
type attachment struct {
	identity sandboxlink.Identity
	runtime  sandboxwire.ID
	lease    time.Time
	timer    *time.Timer
	owner    *attachLink // the link that last opened a stream on it
	bound    bool        // a Bind may have reached the serve peer
	splices  map[*splice]struct{}
}

// splice is one service stream from its Open to its end.
type splice struct {
	att     *attachment
	serve   *serveLink
	al      *attachLink
	attach  *yamux.Stream
	bound   *yamux.Stream // the stream to the serve peer, once opened
	spliced bool          // Opened was sent; an abort resets both streams
	aborted sandboxlink.Code
}

// ServeHTTP upgrades a peer's request to a WebSocket and serves the link until
// it ends.
func (rl *Relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := sandboxlink.UpgradeWebSocket(w, r)
	if err != nil {
		return
	}
	sess, err := sandboxlink.ServerSession(conn)
	if err != nil {
		conn.Close()
		return
	}
	defer sess.Close()
	stop := context.AfterFunc(rl.ctx, func() { sess.Close() })
	defer stop()
	handshake := time.AfterFunc(sandboxlink.HandshakeTimeout, func() { sess.Close() })
	ctl, err := sess.AcceptStream()
	if err != nil {
		return
	}
	id, m, err := sandboxlink.ReadMessage(ctl)
	handshake.Stop()
	l := rl.newLink(sess, ctl)
	switch hello := m.(type) {
	case sandboxlink.ServeHello:
		rl.serve(l, id, hello)
	case sandboxlink.AttachHello:
		rl.attach(l, id, hello)
	default:
		code := sandboxlink.ProtocolViolation
		if errors.Is(err, sandboxlink.VersionMismatch) {
			code = sandboxlink.VersionMismatch
		}
		l.fail(id, sandboxlink.OpHello, code)
	}
	<-sess.CloseChan()
}

func (rl *Relay) authorityContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(rl.ctx, sandboxlink.HandshakeTimeout)
}

// refusal returns the failure code for an Authority error.
func refusal(err error) sandboxlink.Code {
	return sandboxlink.FailureFor(sandboxlink.OpHello, err).Code
}

// serve admits a serve peer as the current one for its resource and holds the
// link until it ends.
func (rl *Relay) serve(l *link, id uint64, hello sandboxlink.ServeHello) {
	sl, err := rl.admitServe(l, id, hello)
	if err != nil {
		l.fail(id, sandboxlink.OpHello, refusal(err))
		return
	}
	key := keyOf(hello.Resource)
	// A serve peer opens no streams and sends nothing after its Hello.
	go func() {
		if st, err := l.sess.AcceptStream(); err == nil {
			st.Reset()
		}
		l.sess.Close()
	}()
	go func() {
		sandboxlink.ReadMessage(l.ctl)
		l.sess.Close()
	}()
	<-l.sess.CloseChan()
	rl.mu.Lock()
	if rl.serves[key] == sl {
		delete(rl.serves, key)
	}
	sl.closures = nil
	rl.mu.Unlock()
}

// admitServe authenticates a serve Hello and installs the link. A revocation
// that lands while the Authority decides forces a fresh decision, so a
// withdrawn credential never installs a peer.
func (rl *Relay) admitServe(l *link, id uint64, hello sandboxlink.ServeHello) (*serveLink, error) {
	for range authorizeAttempts {
		rl.mu.Lock()
		epoch := rl.epoch
		rl.mu.Unlock()
		ctx, cancel := rl.authorityContext()
		peer, err := rl.auth.AuthenticateServe(ctx, hello)
		cancel()
		if err == nil && peer.Resource != hello.Resource {
			err = sandboxlink.Fail(sandboxlink.PermissionDenied)
		}
		if err != nil {
			return nil, err
		}
		rl.mu.Lock()
		if rl.epoch != epoch {
			rl.mu.Unlock()
			continue
		}
		sl, old, err := rl.installServeLocked(l, id, hello)
		rl.mu.Unlock()
		if old != nil {
			old.sess.Close()
		}
		return sl, err
	}
	return nil, sandboxlink.Fail(sandboxlink.ServiceUnavailable)
}

// installServeLocked makes l the resource's serve peer and queues its
// HelloAccepted while the decision is still current; its writer then writes
// the resource's pending AttachmentClosed events. It returns the replaced
// link for the caller to close.
func (rl *Relay) installServeLocked(l *link, id uint64, hello sandboxlink.ServeHello) (sl, old *serveLink, err error) {
	key, generation := keyOf(hello.Resource), hello.Resource.Generation
	if rl.generations[key] > generation {
		return nil, nil, sandboxlink.Fail(sandboxlink.StaleGeneration)
	}
	if rl.generations[key] < generation {
		rl.generations[key] = generation
		delete(rl.closures, key)
		for _, a := range rl.attachments {
			if a.identity.Resource.SameResource(hello.Resource) {
				rl.closeLocked(a, sandboxlink.CloseStaleGeneration)
			}
		}
	}
	if rl.closures[key] == nil {
		rl.closures[key] = closures{}
	}
	sl = &serveLink{link: l, hello: hello}
	sl.closures = rl.closures[key]
	old = rl.serves[key]
	if old != nil {
		old.closures = nil
	}
	rl.serves[key] = sl
	sl.send(id, sandboxlink.HelloAccepted{})
	return sl, old, nil
}

// attach serves an attach peer's control requests and service streams until
// the link ends. Its attachments stay open until their leases expire.
func (rl *Relay) attach(l *link, id uint64, hello sandboxlink.AttachHello) {
	ctx, cancel := rl.authorityContext()
	peer, err := rl.auth.AuthenticateAttach(ctx, hello)
	cancel()
	if err == nil && peer.RuntimeID != hello.RuntimeID {
		err = sandboxlink.Fail(sandboxlink.PermissionDenied)
	}
	if err != nil {
		l.fail(id, sandboxlink.OpHello, refusal(err))
		return
	}
	al := &attachLink{link: l, peer: peer}
	var seq sandboxwire.RequestSequence
	seq.Admit(id) // the Hello takes the first ID
	al.send(id, sandboxlink.HelloAccepted{})
	go func() {
		for {
			st, err := l.sess.AcceptStream()
			if err != nil {
				return
			}
			go rl.open(al, st)
		}
	}()
	for {
		id, m, err := sandboxlink.ReadMessage(l.ctl)
		if err != nil {
			l.sess.Close()
			return
		}
		admitted := seq.Admit(id)
		switch r := m.(type) {
		case sandboxlink.RenewAttachment:
			if !admitted {
				l.fail(id, sandboxlink.OpRenewAttachment, sandboxlink.ProtocolViolation)
				return
			}
			if al.inflight.Add(1) > sandboxlink.MaxControlRequests {
				al.inflight.Add(-1)
				al.send(id, sandboxlink.FailureFor(sandboxlink.OpRenewAttachment, sandboxlink.Fail(sandboxlink.LimitExceeded)))
				continue
			}
			go func() {
				defer al.inflight.Add(-1)
				rl.renew(al, id, r)
			}()
		case sandboxlink.CloseAttachment:
			if !admitted {
				l.fail(id, sandboxlink.OpCloseAttachment, sandboxlink.ProtocolViolation)
				return
			}
			rl.closeRequested(al, id, r)
		case sandboxlink.ServeHello, sandboxlink.AttachHello:
			l.fail(id, sandboxlink.OpHello, sandboxlink.ProtocolViolation)
			return
		case sandboxlink.Open:
			l.fail(id, sandboxlink.OpOpen, sandboxlink.ProtocolViolation)
			return
		case sandboxlink.Bind:
			l.fail(id, sandboxlink.OpBind, sandboxlink.ProtocolViolation)
			return
		default:
			l.sess.Close()
			return
		}
	}
}

func (rl *Relay) renew(al *attachLink, id uint64, r sandboxlink.RenewAttachment) {
	renewed, err := rl.renewLease(al, r)
	if err != nil {
		al.send(id, sandboxlink.FailureFor(sandboxlink.OpRenewAttachment, err))
		return
	}
	al.send(id, renewed)
}

func (rl *Relay) renewLease(al *attachLink, r sandboxlink.RenewAttachment) (sandboxlink.AttachmentRenewed, error) {
	rl.mu.Lock()
	a := rl.attachments[r.AttachmentID]
	rl.mu.Unlock()
	if a == nil {
		return sandboxlink.AttachmentRenewed{}, sandboxlink.Fail(sandboxlink.LeaseExpired)
	}
	if a.runtime != al.peer.RuntimeID {
		return sandboxlink.AttachmentRenewed{}, sandboxlink.Fail(sandboxlink.PermissionDenied)
	}
	ctx, cancel := rl.authorityContext()
	auth, err := rl.auth.Renew(ctx, al.peer, r)
	cancel()
	if err != nil {
		return sandboxlink.AttachmentRenewed{}, err
	}
	if auth.Validate(nil) != nil {
		return sandboxlink.AttachmentRenewed{}, sandboxlink.Fail(sandboxlink.ServiceUnavailable)
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	switch {
	case rl.attachments[r.AttachmentID] != a || !auth.LeaseExpiresAt.After(time.Now()):
		return sandboxlink.AttachmentRenewed{}, sandboxlink.Fail(sandboxlink.LeaseExpired)
	case auth.Identity != a.identity:
		return sandboxlink.AttachmentRenewed{}, sandboxlink.Fail(sandboxlink.AttachmentConflict)
	}
	rl.setLeaseLocked(a, auth.LeaseExpiresAt)
	return sandboxlink.AttachmentRenewed{AttachmentID: r.AttachmentID, LeaseExpiresAt: auth.LeaseExpiresAt}, nil
}

// closeRequested closes an attachment for its own Runtime. Closing an unknown
// attachment succeeds, so a retried close is harmless.
func (rl *Relay) closeRequested(al *attachLink, id uint64, c sandboxlink.CloseAttachment) {
	rl.mu.Lock()
	a := rl.attachments[c.AttachmentID]
	denied := a != nil && a.runtime != al.peer.RuntimeID
	if a != nil && !denied {
		rl.closeLocked(a, sandboxlink.CloseRequested)
	}
	rl.mu.Unlock()
	if denied {
		al.send(id, sandboxlink.FailureFor(sandboxlink.OpCloseAttachment, sandboxlink.Fail(sandboxlink.PermissionDenied)))
		return
	}
	al.send(id, sandboxlink.CloseAccepted{})
}

// closeLocked ends an attachment: it aborts its streams and tells the attach
// peer and the serve peer why. A serve peer that is away when its attachment
// closes hears of it when the same generation reconnects.
func (rl *Relay) closeLocked(a *attachment, reason sandboxlink.CloseReason) {
	id := a.identity.AttachmentID
	delete(rl.attachments, id)
	a.timer.Stop()
	for sp := range a.splices {
		sp.abortLocked(abortCodes[reason])
	}
	if reason != sandboxlink.CloseRequested {
		a.owner.closedLocked(id, reason)
	}
	key, generation := keyOf(a.identity.Resource), a.identity.Resource.Generation
	if !a.bound || rl.generations[key] != generation {
		return
	}
	if sl := rl.serves[key]; sl != nil {
		sl.closedLocked(id, reason)
		return
	}
	if rl.closures[key] == nil {
		rl.closures[key] = closures{}
	}
	rl.closures[key][id] = reason
}

// abortCodes answers an Open that an attachment's close interrupts.
var abortCodes = map[sandboxlink.CloseReason]sandboxlink.Code{
	sandboxlink.CloseRequested:       sandboxlink.AttachmentConflict,
	sandboxlink.CloseLeaseExpired:    sandboxlink.LeaseExpired,
	sandboxlink.CloseRevoked:         sandboxlink.PermissionDenied,
	sandboxlink.CloseStaleGeneration: sandboxlink.StaleGeneration,
}

func (rl *Relay) setLeaseLocked(a *attachment, lease time.Time) {
	a.lease = lease
	if a.timer != nil {
		a.timer.Stop()
	}
	a.timer = time.AfterFunc(time.Until(lease), func() {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		if rl.attachments[a.identity.AttachmentID] == a && !time.Now().Before(a.lease) {
			rl.closeLocked(a, sandboxlink.CloseLeaseExpired)
		}
	})
}

// abortLocked records why a splice ends and resets its streams. Before Opened
// the opening goroutine answers the attach peer with code instead.
func (sp *splice) abortLocked(code sandboxlink.Code) {
	if sp.aborted != 0 {
		return
	}
	sp.aborted = code
	if sp.bound != nil {
		go sp.bound.Reset()
	}
	if sp.spliced {
		go sp.attach.Reset()
	}
}

// open handles one service stream from an attach peer: it reads Open,
// authorizes it, binds the serve peer and splices the two streams.
func (rl *Relay) open(al *attachLink, st *yamux.Stream) {
	st.SetDeadline(time.Now().Add(sandboxlink.HandshakeTimeout))
	id, m, err := sandboxlink.ReadMessage(st)
	st.SetDeadline(time.Time{})
	o, ok := m.(sandboxlink.Open)
	if err != nil || !ok {
		refuse(st, id, sandboxlink.Fail(sandboxlink.ProtocolViolation))
		return
	}
	sp, auth, err := rl.admit(al, st, o)
	if err != nil {
		refuse(st, id, err)
		return
	}
	defer rl.finish(sp)
	sl := sp.serve
	opened := sandboxlink.Opened{AttachmentID: o.AttachmentID, ServerInstanceID: sl.hello.ServerInstanceID,
		LeaseExpiresAt: auth.LeaseExpiresAt}
	bind := sandboxlink.Bind{AttachmentID: o.AttachmentID, Service: o.Service, Version: o.Version,
		SessionID: o.SessionID, AssignmentID: o.AssignmentID, AssignmentEpoch: o.AssignmentEpoch,
		LeaseExpiresAt: auth.LeaseExpiresAt, ExpectedServerInstanceID: sl.hello.ServerInstanceID,
		Exports: auth.Exports, Egress: auth.Egress}
	err = rl.bind(sp, bind)
	rl.mu.Lock()
	if sp.aborted != 0 {
		err = abortError(sp.aborted, err)
	}
	if err == nil {
		sp.spliced = true
	}
	rl.mu.Unlock()
	if err != nil {
		if sp.bound != nil {
			sp.bound.Reset()
		}
		refuse(st, id, err)
		return
	}
	if err := sandboxlink.WriteMessage(st, id, opened); err != nil {
		rl.abort(sp)
	}
	rl.splice(sp)
}

// admit authorizes o and registers its splice. A revocation that lands while
// the Authority decides forces a fresh decision.
func (rl *Relay) admit(al *attachLink, st *yamux.Stream, o sandboxlink.Open) (*splice, sandboxlink.Authorization, error) {
	for range authorizeAttempts {
		rl.mu.Lock()
		epoch := rl.epoch
		rl.mu.Unlock()
		ctx, cancel := rl.authorityContext()
		auth, err := rl.auth.AuthorizeOpen(ctx, al.peer, o)
		cancel()
		if err != nil {
			return nil, auth, err
		}
		if auth.Validate(&o) != nil {
			return nil, auth, sandboxlink.Fail(sandboxlink.ServiceUnavailable)
		}
		rl.mu.Lock()
		if rl.epoch != epoch {
			rl.mu.Unlock()
			continue
		}
		sp, err := rl.admitLocked(al, st, o, auth)
		rl.mu.Unlock()
		return sp, auth, err
	}
	return nil, sandboxlink.Authorization{}, sandboxlink.Fail(sandboxlink.ServiceUnavailable)
}

func (rl *Relay) admitLocked(al *attachLink, st *yamux.Stream, o sandboxlink.Open, auth sandboxlink.Authorization) (*splice, error) {
	key, generation := keyOf(o.Resource), o.Resource.Generation
	sl := rl.serves[key]
	var offered *sandboxlink.ServiceVersion
	if sl != nil {
		for i, s := range sl.hello.Services {
			if s.Service == o.Service {
				offered = &sl.hello.Services[i]
			}
		}
	}
	a := rl.attachments[o.AttachmentID]
	var code sandboxlink.Code
	switch {
	case al.streams >= maxStreams || (sl != nil && sl.streams >= maxStreams):
		code = sandboxlink.LimitExceeded
	case rl.generations[key] > generation:
		code = sandboxlink.StaleGeneration
	case sl == nil || sl.hello.Resource.Generation != generation || offered == nil:
		code = sandboxlink.ServiceUnavailable
	case offered.Version != o.Version:
		code = sandboxlink.VersionMismatch
	case !o.ExpectedServerInstanceID.IsZero() && o.ExpectedServerInstanceID != sl.hello.ServerInstanceID:
		code = sandboxlink.InstanceChanged
	case !auth.LeaseExpiresAt.After(time.Now()):
		code = sandboxlink.LeaseExpired
	case a != nil && (a.identity != o.Identity() || a.runtime != al.peer.RuntimeID):
		code = sandboxlink.AttachmentConflict
	}
	if code != 0 {
		return nil, sandboxlink.Fail(code)
	}
	if a == nil {
		a = &attachment{identity: o.Identity(), runtime: al.peer.RuntimeID, splices: map[*splice]struct{}{}}
		rl.attachments[o.AttachmentID] = a
	}
	a.owner = al
	a.bound = true
	rl.setLeaseLocked(a, auth.LeaseExpiresAt)
	sp := &splice{att: a, serve: sl, al: al, attach: st}
	a.splices[sp] = struct{}{}
	al.streams++
	sl.streams++
	return sp, nil
}

// bind opens the stream to the serve peer and exchanges Bind and Bound. Once
// the Bind began to be sent, a failure without the serve peer's answer is
// uncertain: the serve peer may have bound the attachment.
func (rl *Relay) bind(sp *splice, b sandboxlink.Bind) error {
	ctx, cancel := context.WithTimeout(rl.ctx, sandboxlink.HandshakeTimeout)
	ss, err := sp.serve.sess.OpenStream(ctx)
	cancel()
	if err != nil {
		return err
	}
	rl.mu.Lock()
	sp.bound = ss
	aborted := sp.aborted
	rl.mu.Unlock()
	if aborted != 0 {
		return sandboxlink.Fail(aborted)
	}
	var seq sandboxwire.RequestSequence
	f, err := sandboxlink.Encode(seq.Next(), b)
	if err != nil {
		return err
	}
	ss.SetDeadline(time.Now().Add(sandboxlink.HandshakeTimeout))
	if err := sandboxwire.WriteFrame(ss, f); err != nil {
		return sandboxlink.Uncertain(err)
	}
	if _, err := sandboxlink.ReadReply(ss, sandboxlink.OpBind, f.RequestID); err != nil {
		return err
	}
	ss.SetDeadline(time.Time{})
	return nil
}

// abortError answers an Open that a close interrupted during bind, whose own
// result was err. It keeps the uncertainty of a Bind that may have reached the
// serve peer: only a Bind never sent or definitively refused had no effect.
func abortError(code sandboxlink.Code, err error) error {
	effect := sandboxwire.EffectNone
	var e *sandboxlink.Error
	if err == nil || errors.As(err, &e) && e.Effect == sandboxwire.EffectPossible {
		effect = sandboxwire.EffectPossible
	}
	return &sandboxlink.Error{Code: code, Effect: effect}
}

func (rl *Relay) abort(sp *splice) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	sp.abortLocked(sandboxlink.ServiceUnavailable)
}

func (rl *Relay) finish(sp *splice) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(sp.att.splices, sp)
	sp.al.streams--
	sp.serve.streams--
}

// splice copies both directions until each ends. An orderly end (FIN) is
// passed on as CloseWrite once every byte before it is written; any error,
// from either stream or from an abort, resets both streams. The loss of
// either link aborts the splice even while a pump is blocked writing to the
// other peer.
func (rl *Relay) splice(sp *splice) {
	done := make(chan struct{})
	go func() {
		select {
		case <-sp.serve.sess.CloseChan():
		case <-sp.al.sess.CloseChan():
		case <-done:
			return
		}
		rl.abort(sp)
	}()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rl.pump(sp, sp.bound, sp.attach)
	}()
	rl.pump(sp, sp.attach, sp.bound)
	wg.Wait()
	close(done)
}

func (rl *Relay) pump(sp *splice, dst, src *yamux.Stream) {
	buf := make([]byte, spliceBuffer)
	if _, err := io.CopyBuffer(struct{ io.Writer }{dst}, struct{ io.Reader }{src}, buf); err != nil {
		rl.abort(sp)
		return
	}
	if err := dst.CloseWrite(); err != nil {
		rl.abort(sp)
	}
}

// refuse answers an Open with a failure and ends the stream in order.
func refuse(st *yamux.Stream, id uint64, err error) {
	if sandboxlink.WriteMessage(st, id, sandboxlink.FailureFor(sandboxlink.OpOpen, err)) != nil {
		st.Reset()
		return
	}
	st.Close()
}
