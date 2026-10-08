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
	// maxStreams bounds the concurrent service streams of each link. An attach
	// link's stream counts from its Open, through the Authority's decision.
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

// The relay's capacity, for one Core serving one deployment, which runs at
// most 1024 executions at once (the maximum OAC_EXECUTION_CONCURRENCY).
// Measured idle, the relay spends about 64 KiB on a serve link, 48 KiB on an
// attach link and 1 KiB on an attachment, so at capacity they hold under
// 500 MiB.
const (
	// maxResources bounds the resources held, and with them serve links,
	// one per resource. It leaves room for idle sandboxes beside busy ones.
	maxResources = 4096
	// maxServeHellos bounds the serve Hellos being decided for one resource,
	// so a redial or a newer generation's peer can arrive while one is.
	maxServeHellos = 2
	// maxAttachments bounds attachments, open or with AttachmentClosed events
	// to write: four per resource.
	maxAttachments = 16384
	// maxAttachLinks bounds attach links. An agent host dials one per
	// attachment it uses, a few per execution.
	maxAttachLinks = 4096
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
	resources   map[resourceKey]*resource
	attachments map[sandboxwire.ID]*attachment
	// held counts attachment slots: open attachments, and closed ones whose
	// AttachmentClosed events are not yet written or discarded.
	held        int
	attachLinks int
	// The capacity of each bounded kind. New sets the constants above; tests
	// lower them.
	maxResources, maxAttachments, maxAttachLinks int
}

// resource is what the relay holds for one resource. It takes one of
// maxResources from the serve Hello that creates it until it holds nothing:
// no serve peer, Hello being decided, attachment or event.
type resource struct {
	generation uint64     // the newest generation seen
	serve      *serveLink // nil while the serve peer is away
	// closures holds the AttachmentClosed events not yet written to the serve
	// peer of generation, connected or not.
	closures    closures
	hellos      int // serve Hellos being decided, at most maxServeHellos
	attachments int // open attachments
}

// closures is a set of AttachmentClosed events to write, by attachment ID.
// Each event keeps its attachment's slot until it is written or discarded.
type closures map[sandboxwire.ID]*attachment

// New returns a relay that asks auth to authenticate peers and authorize their
// requests.
func New(auth sandboxlink.Authority) *Relay {
	ctx, cancel := context.WithCancel(context.Background())
	return &Relay{auth: auth, ctx: ctx, cancel: cancel,
		maxResources: maxResources, maxAttachments: maxAttachments, maxAttachLinks: maxAttachLinks,
		resources:   map[resourceKey]*resource{},
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
// The caller withdraws the authority first, so the revoked generation cannot
// reconnect, and the events it could not read are discarded.
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
	r := rl.resources[key]
	if r == nil || r.generation > ref.Generation {
		return
	}
	if sl := r.serve; sl != nil {
		// The link keeps the set to write before it ends.
		r.serve = nil
		sl.end()
	} else {
		rl.dropLocked(r.closures)
	}
	r.closures = closures{}
	rl.releaseLocked(key, r)
}

// Serving reports whether the relay holds a serve peer of ref at
// ref.Generation. It is this process's view, held in memory like the rest of
// the relay.
func (rl *Relay) Serving(ref sandboxlink.ResourceRef) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	r := rl.resources[keyOf(ref)]
	return r != nil && r.serve != nil && r.serve.hello.Resource.Generation == ref.Generation
}

type resourceKey struct {
	tenant, environment, id sandboxwire.ID
	kind                    sandboxlink.ResourceKind
}

func keyOf(r sandboxlink.ResourceRef) resourceKey {
	return resourceKey{tenant: r.TenantID, environment: r.EnvironmentID, id: r.ID, kind: r.Kind}
}

// link is one authenticated connection. Its writer sends answers from a
// bounded queue and AttachmentClosed events from a set, so the relay never
// blocks on a peer while holding its lock: an event leaves the set once it is
// written, or with the set when no link can write it any more.
type link struct {
	sess *yamux.Session
	ctl  *yamux.Stream
	out  chan outgoing
	wake chan struct{} // the set has events to write
	// Under Relay.mu: the events to write, nil once the link no longer
	// writes them, and the open service streams. A serve link shares its
	// resource's set, which outlives the link.
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
		var a *attachment
		for _, a = range l.closures {
			break
		}
		rl.mu.Unlock()
		if a == nil {
			return nil
		}
		id := a.identity.AttachmentID
		if err := sandboxlink.WriteMessage(l.ctl, 0, sandboxlink.AttachmentClosed{AttachmentID: id, Reason: a.reason}); err != nil {
			return err
		}
		rl.mu.Lock()
		if l.closures[id] == a {
			delete(l.closures, id)
			rl.unrefLocked(a)
		}
		rl.mu.Unlock()
	}
}

// queueLocked adds a's AttachmentClosed event to set, unless set is nil. An
// unwritten event of an earlier attachment with the same ID gives way to it.
func (rl *Relay) queueLocked(set closures, a *attachment) {
	if set == nil {
		return
	}
	id := a.identity.AttachmentID
	if old := set[id]; old != nil {
		rl.unrefLocked(old)
	}
	set[id] = a
	a.refs++
}

// dropLocked discards the events of a set that no link will write.
func (rl *Relay) dropLocked(set closures) {
	for _, a := range set {
		rl.unrefLocked(a)
	}
}

// unrefLocked drops one of a's references, the open attachment or one of its
// events. The last frees its slot.
func (rl *Relay) unrefLocked(a *attachment) {
	a.refs--
	if a.refs == 0 {
		rl.held--
	}
}

// releaseLocked forgets r, freeing its slot, once it holds nothing.
func (rl *Relay) releaseLocked(key resourceKey, r *resource) {
	if r.serve == nil && r.hellos == 0 && r.attachments == 0 && len(r.closures) == 0 {
		delete(rl.resources, key)
	}
}

// wakeWriter tells l's writer that its set has events.
func (l *link) wakeWriter() {
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
// that carry its streams until its lease expires or it is closed, and keeps
// one of maxAttachments until its AttachmentClosed events are written or
// discarded.
type attachment struct {
	identity sandboxlink.Identity
	runtime  sandboxwire.ID
	lease    time.Time
	timer    *time.Timer
	owner    *attachLink // the link that last opened a stream on it
	bound    bool        // a Bind may have reached the serve peer
	splices  map[*splice]struct{}
	reason   sandboxlink.CloseReason // why it closed; zero while open
	refs     int                     // its slot's holders: the open attachment and its unwritten events
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
	defer rl.mu.Unlock()
	if r := rl.resources[key]; r != nil && r.serve == sl {
		// The resource keeps the set for a reconnect.
		r.serve = nil
		rl.releaseLocked(key, r)
	} else {
		// A replaced link has no set; a revoked one takes it along.
		rl.dropLocked(sl.closures)
	}
	sl.closures = nil
}

// admitServe authenticates a serve Hello and installs the link. Until it is
// decided, the Hello takes one of its resource's maxServeHellos, and a
// resource slot when the relay does not hold the resource. A revocation that
// lands while the Authority decides forces a fresh decision, so a withdrawn
// credential never installs a peer.
func (rl *Relay) admitServe(l *link, id uint64, hello sandboxlink.ServeHello) (*serveLink, error) {
	key := keyOf(hello.Resource)
	rl.mu.Lock()
	r := rl.resources[key]
	if r == nil && len(rl.resources) >= rl.maxResources || r != nil && r.hellos >= maxServeHellos {
		rl.mu.Unlock()
		return nil, sandboxlink.Fail(sandboxlink.LimitExceeded)
	}
	if r == nil {
		r = &resource{closures: closures{}}
		rl.resources[key] = r
	}
	r.hellos++
	rl.mu.Unlock()
	defer func() {
		rl.mu.Lock()
		r.hellos--
		rl.releaseLocked(key, r)
		rl.mu.Unlock()
	}()
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
		sl, old, err := rl.installServeLocked(r, l, id, hello)
		rl.mu.Unlock()
		if old != nil {
			old.sess.Close()
		}
		return sl, err
	}
	return nil, sandboxlink.Fail(sandboxlink.ServiceUnavailable)
}

// installServeLocked makes l r's serve peer and queues its HelloAccepted
// while the decision is still current; its writer then writes r's pending
// AttachmentClosed events. It returns the replaced link for the caller to
// close.
func (rl *Relay) installServeLocked(r *resource, l *link, id uint64, hello sandboxlink.ServeHello) (sl, old *serveLink, err error) {
	generation := hello.Resource.Generation
	if r.generation > generation {
		return nil, nil, sandboxlink.Fail(sandboxlink.StaleGeneration)
	}
	if r.generation < generation {
		r.generation = generation
		rl.dropLocked(r.closures)
		r.closures = closures{}
		for _, a := range rl.attachments {
			if a.identity.Resource.SameResource(hello.Resource) {
				rl.closeLocked(a, sandboxlink.CloseStaleGeneration)
			}
		}
	}
	sl = &serveLink{link: l, hello: hello}
	sl.closures = r.closures
	if old = r.serve; old != nil {
		old.closures = nil
	}
	r.serve = sl
	sl.send(id, sandboxlink.HelloAccepted{})
	return sl, old, nil
}

// attach serves an attach peer's control requests and service streams until
// the link ends. The link takes an attach link slot first and keeps it until
// every stream and renewal it carried has finished, so the decisions of a
// dropped link stay bounded. Its attachments stay open until their leases
// expire.
func (rl *Relay) attach(l *link, id uint64, hello sandboxlink.AttachHello) {
	rl.mu.Lock()
	full := rl.attachLinks >= rl.maxAttachLinks
	if !full {
		rl.attachLinks++
		l.closures = closures{}
	}
	rl.mu.Unlock()
	if full {
		l.fail(id, sandboxlink.OpHello, sandboxlink.LimitExceeded)
		return
	}
	var serving sync.WaitGroup
	defer func() {
		<-l.sess.CloseChan()
		serving.Wait()
		rl.mu.Lock()
		rl.attachLinks--
		rl.dropLocked(l.closures)
		l.closures = nil
		rl.mu.Unlock()
	}()
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
	serving.Go(func() {
		for {
			st, err := l.sess.AcceptStream()
			if err != nil {
				return
			}
			serving.Go(func() { rl.open(al, st) })
		}
	})
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
			serving.Go(func() {
				defer al.inflight.Add(-1)
				rl.renew(al, id, r)
			})
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
	delete(rl.attachments, a.identity.AttachmentID)
	a.timer.Stop()
	for sp := range a.splices {
		sp.abortLocked(abortCodes[reason])
	}
	a.reason = reason
	if reason != sandboxlink.CloseRequested {
		rl.queueLocked(a.owner.closures, a)
		a.owner.wakeWriter()
	}
	key := keyOf(a.identity.Resource)
	r := rl.resources[key] // the attachment holds it
	if a.bound && r.generation == a.identity.Resource.Generation {
		rl.queueLocked(r.closures, a)
		if r.serve != nil {
			r.serve.wakeWriter()
		}
	}
	r.attachments--
	rl.releaseLocked(key, r)
	rl.unrefLocked(a)
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

// admit authorizes o and registers its splice. Before the Authority decides,
// the Open takes a stream slot of its link, which an admitted stream keeps
// until finish and a refused one gives back once it is decided, whether or
// not the peer reset it. An Open of an attachment the relay does not hold
// also takes an attachment slot. A revocation that lands while the Authority
// decides forces a fresh decision.
func (rl *Relay) admit(al *attachLink, st *yamux.Stream, o sandboxlink.Open) (sp *splice, auth sandboxlink.Authorization, err error) {
	rl.mu.Lock()
	prior := rl.attachments[o.AttachmentID]
	reserved := prior == nil
	if al.streams >= maxStreams || reserved && rl.held >= rl.maxAttachments {
		rl.mu.Unlock()
		return nil, auth, sandboxlink.Fail(sandboxlink.LimitExceeded)
	}
	al.streams++
	if reserved {
		rl.held++
	}
	rl.mu.Unlock()
	defer func() {
		rl.mu.Lock()
		if sp == nil {
			al.streams--
		}
		if reserved {
			rl.held--
		}
		rl.mu.Unlock()
	}()
	for range authorizeAttempts {
		rl.mu.Lock()
		epoch := rl.epoch
		rl.mu.Unlock()
		ctx, cancel := rl.authorityContext()
		auth, err = rl.auth.AuthorizeOpen(ctx, al.peer, o)
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
		sp, err = rl.admitLocked(al, st, o, auth, prior, &reserved)
		rl.mu.Unlock()
		return sp, auth, err
	}
	return nil, sandboxlink.Authorization{}, sandboxlink.Fail(sandboxlink.ServiceUnavailable)
}

// admitLocked checks o against what the relay holds and registers its
// splice. A new attachment takes the slot reserved for it. The attachment
// prior, which the relay held when the Open arrived, must still be the one
// under its ID: once it closed, the Open fails even if another attachment
// took the ID since.
func (rl *Relay) admitLocked(al *attachLink, st *yamux.Stream, o sandboxlink.Open, auth sandboxlink.Authorization, prior *attachment, reserved *bool) (*splice, error) {
	key, generation := keyOf(o.Resource), o.Resource.Generation
	r := rl.resources[key]
	var sl *serveLink
	if r != nil {
		sl = r.serve
	}
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
	case sl != nil && sl.streams >= maxStreams:
		code = sandboxlink.LimitExceeded
	case r != nil && r.generation > generation:
		code = sandboxlink.StaleGeneration
	case sl == nil || sl.hello.Resource.Generation != generation || offered == nil:
		code = sandboxlink.ServiceUnavailable
	case offered.Version != o.Version:
		code = sandboxlink.VersionMismatch
	case !o.ExpectedServerInstanceID.IsZero() && o.ExpectedServerInstanceID != sl.hello.ServerInstanceID:
		code = sandboxlink.InstanceChanged
	case !auth.LeaseExpiresAt.After(time.Now()) || prior != nil && a != prior:
		code = sandboxlink.LeaseExpired
	case a != nil && (a.identity != o.Identity() || a.runtime != al.peer.RuntimeID):
		code = sandboxlink.AttachmentConflict
	}
	if code != 0 {
		return nil, sandboxlink.Fail(code)
	}
	if a == nil {
		a = &attachment{identity: o.Identity(), runtime: al.peer.RuntimeID, splices: map[*splice]struct{}{}, refs: 1}
		rl.attachments[o.AttachmentID] = a
		r.attachments++
		*reserved = false
	}
	a.owner = al
	a.bound = true
	rl.setLeaseLocked(a, auth.LeaseExpiresAt)
	sp := &splice{att: a, serve: sl, al: al, attach: st}
	a.splices[sp] = struct{}{}
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
