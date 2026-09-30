package sandboxlink

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/libp2p/go-yamux/v5"
)

// AttachConfig configures an attach peer. Dial replaces the WebSocket dial to
// URL in tests. OnAttachmentClosed runs on the link's reader and must not
// block.
type AttachConfig struct {
	URL                string
	TLS                *tls.Config
	Dial               Dialer
	RuntimeID          sandboxwire.ID
	Credential         []byte
	OnAttachmentClosed func(AttachmentClosed)
}

// AttachLink is an attach peer's authenticated link to the relay.
type AttachLink struct {
	sess     *yamux.Session
	ctl      *yamux.Stream
	accepted HelloAccepted
	onClosed func(AttachmentClosed)
	done     chan struct{}

	// write holds the one control write slot. The holder allocates the next
	// request ID and writes its frame, so IDs increase in wire order.
	write chan struct{}
	seq   sandboxwire.RequestSequence

	mu      sync.Mutex
	pending map[uint64]chan Message
	err     error
}

// ErrLinkClosed is returned for requests on a link that has ended.
var ErrLinkClosed = errors.New("sandbox link: link closed")

// DialAttach connects to the relay and authenticates as cfg.RuntimeID.
func DialAttach(ctx context.Context, cfg AttachConfig) (*AttachLink, error) {
	hello := AttachHello{Version: Version, RuntimeID: cfg.RuntimeID, Credential: cfg.Credential}
	if _, err := Encode(1, hello); err != nil {
		return nil, err
	}
	l := &AttachLink{onClosed: cfg.OnAttachmentClosed, done: make(chan struct{}), write: make(chan struct{}, 1),
		pending: map[uint64]chan Message{}}
	sess, ctl, accepted, err := connect(ctx, dialerFor(cfg.Dial, cfg.URL, cfg.TLS), hello, &l.seq)
	if err != nil {
		return nil, err
	}
	l.sess, l.ctl, l.accepted = sess, ctl, accepted
	go l.read()
	return l, nil
}

// Accepted returns the relay's HelloAccepted.
func (l *AttachLink) Accepted() HelloAccepted { return l.accepted }

// Done is closed when the link ends.
func (l *AttachLink) Done() <-chan struct{} { return l.done }

// Close ends the link and every stream on it. Attachments stay open at the
// relay until their leases expire or a later link closes them.
func (l *AttachLink) Close() error { return l.sess.Close() }

// OpenService opens a service stream and waits for Opened. A refusal returns
// the relay's *Error; a failure after Open began to be sent returns an *Error
// with EffectPossible, because the attachment may exist. ctx bounds the open;
// the returned stream outlives it.
func (l *AttachLink) OpenService(ctx context.Context, o Open) (Stream, Opened, error) {
	if _, err := Encode(1, o); err != nil {
		return nil, Opened{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, Opened{}, notSent(err)
	}
	st, err := l.sess.OpenStream(ctx)
	if err != nil {
		return nil, Opened{}, err
	}
	stop := context.AfterFunc(ctx, func() { st.Reset() })
	opened, err := func() (Opened, error) {
		var seq sandboxwire.RequestSequence
		if err := WriteMessage(st, seq.Next(), o); err != nil {
			return Opened{}, Uncertain(err)
		}
		_, m, err := ReadMessage(st, MaxMessageBytes)
		if err != nil {
			return Opened{}, Uncertain(err)
		}
		switch r := m.(type) {
		case Opened:
			if r.AttachmentID == o.AttachmentID {
				return r, nil
			}
		case Failure:
			return Opened{}, r.Err()
		}
		return Opened{}, errPossibleViolation
	}()
	if !stop() {
		err = Uncertain(ctx.Err())
	}
	if err != nil {
		st.Reset()
		return nil, Opened{}, err
	}
	return st, opened, nil
}

// Renew extends an attachment's lease with a current grant.
func (l *AttachLink) Renew(ctx context.Context, r RenewAttachment) (AttachmentRenewed, error) {
	m, err := l.call(ctx, r)
	if err != nil {
		return AttachmentRenewed{}, err
	}
	renewed, ok := m.(AttachmentRenewed)
	if !ok || renewed.AttachmentID != r.AttachmentID {
		return AttachmentRenewed{}, errPossibleViolation
	}
	return renewed, nil
}

// CloseAttachment ends an attachment and all its streams. Closing an unknown
// attachment succeeds.
func (l *AttachLink) CloseAttachment(ctx context.Context, id sandboxwire.ID) error {
	m, err := l.call(ctx, CloseAttachment{AttachmentID: id})
	if err != nil {
		return err
	}
	if _, ok := m.(CloseAccepted); !ok {
		return errPossibleViolation
	}
	return nil
}

// errPossibleViolation answers a request whose response did not fit it.
var errPossibleViolation = &Error{Code: ProtocolViolation, Effect: sandboxwire.EffectPossible}

// Write states of a control request. Whichever of the writer and a
// cancellation leaves writing first decides the request's fate.
const (
	writing int32 = iota
	written
	cancelled
)

// notSent is the failure of a request that ended before any of it was sent.
func notSent(err error) *Error {
	return &Error{Code: ServiceUnavailable, Effect: sandboxwire.EffectNone, Cause: err}
}

// call sends a control request and waits for its response. A Failure returns
// its *Error. A ctx that ends before the request is sent returns an *Error
// with EffectNone and leaves the link up; the link's end returns
// ErrLinkClosed. Once its frame began to be sent, a failure returns an *Error
// with EffectPossible; a write that ctx interrupts or that fails ends the
// link, because the control stream may hold a partial frame.
func (l *AttachLink) call(ctx context.Context, req Message) (Message, error) {
	if _, err := Encode(1, req); err != nil {
		return nil, err
	}
	select {
	case l.write <- struct{}{}:
	case <-ctx.Done():
		return nil, notSent(ctx.Err())
	case <-l.done:
		return nil, ErrLinkClosed
	}
	// select may pick the free slot over a context that had already ended.
	if err := ctx.Err(); err != nil {
		<-l.write
		return nil, notSent(err)
	}
	l.mu.Lock()
	if l.err != nil {
		l.mu.Unlock()
		<-l.write
		return nil, l.err
	}
	id := l.seq.Next()
	ch := make(chan Message, 1)
	l.pending[id] = ch
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.pending, id)
		l.mu.Unlock()
	}()
	// A cancellation while the frame is being written ends the link, because
	// the control stream cannot carry a partial frame. Once the frame is
	// written, a cancellation only stops the wait for the answer.
	var state atomic.Int32
	stop := context.AfterFunc(ctx, func() {
		if state.CompareAndSwap(writing, cancelled) {
			l.sess.Close()
		}
	})
	err := WriteMessage(l.ctl, id, req)
	if !state.CompareAndSwap(writing, written) {
		// The cancellation won: the link is ending, and ends before the slot
		// is released so no later request is written on it.
		err = ctx.Err()
	}
	stop()
	if err != nil {
		l.sess.Close()
		<-l.write
		return nil, Uncertain(err)
	}
	<-l.write
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, Uncertain(ErrLinkClosed)
		}
		if f, failed := m.(Failure); failed {
			return nil, f.Err()
		}
		if m.frameType() != sandboxwire.ResponseType(req.frameType()) {
			return nil, errPossibleViolation
		}
		return m, nil
	case <-ctx.Done():
		return nil, Uncertain(ctx.Err())
	}
}

// read dispatches control messages until the link ends. A request from the
// relay or an unreadable frame ends the link.
func (l *AttachLink) read() {
	defer func() {
		l.sess.Close()
		l.mu.Lock()
		l.err = ErrLinkClosed
		for id, ch := range l.pending {
			close(ch)
			delete(l.pending, id)
		}
		l.mu.Unlock()
		close(l.done)
	}()
	for {
		id, m, err := ReadMessage(l.ctl, MaxMessageBytes)
		if err != nil {
			return
		}
		switch r := m.(type) {
		case AttachmentClosed:
			if l.onClosed != nil {
				l.onClosed(r)
			}
		default:
			if !sandboxwire.IsResponse(m.frameType()) {
				return
			}
			l.mu.Lock()
			if ch := l.pending[id]; ch != nil {
				ch <- m
				delete(l.pending, id)
			}
			l.mu.Unlock()
		}
	}
}
