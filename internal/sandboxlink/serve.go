package sandboxlink

import (
	"context"
	"crypto/tls"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/libp2p/go-yamux/v5"
)

// ServiceHandler serves one service. Serve owns the stream and returns when
// it is done with it. ctx ends when the attachment closes or Serve returns.
// The Bind carries the authorized binding, including a File stream's exports.
// seq is the stream's bind sequence. Bind order is the order in which the
// serve peer assigns it, under the lock that tracks attachments and before it
// answers Bound; concurrent Bound and Opened replies may reach the opener in
// another order, and a handler that runs late keeps its stream's place. The
// sequence belongs to one Serve call and survives reconnects; it increases
// strictly but has gaps, since all attachments share it. A service may rely
// on it to fence a stream's successor.
type ServiceHandler struct {
	Service Service
	Version uint16
	Serve   func(ctx context.Context, b Bind, seq uint64, s Stream)
}

// ServeConfig configures a serve peer. Dial replaces the WebSocket dial to URL
// in tests. The callbacks run in event order on the link's goroutines and must
// not block.
type ServeConfig struct {
	URL              string
	TLS              *tls.Config
	Dial             Dialer
	Credential       []byte
	Resource         ResourceRef
	ServerInstanceID sandboxwire.ID
	Services         []ServiceHandler

	// OnConnected reports each accepted Hello; OnDisconnected reports why a
	// link ended before the next attempt.
	OnConnected    func()
	OnDisconnected func(error)
	// An attachment is lost when its last open stream ends while it is still
	// attached, restored when a stream binds it again, and closed when the
	// relay reports AttachmentClosed. A lost attachment may be closed without
	// being restored. Closed is final: a Bind for a recently closed
	// attachment is refused with LeaseExpired.
	OnAttachmentLost     func(sandboxwire.ID)
	OnAttachmentRestored func(sandboxwire.ID)
	OnAttachmentClosed   func(sandboxwire.ID, CloseReason)

	// MinBackoff and MaxBackoff bound the jittered reconnect delay; zero
	// selects 100ms and 30s.
	MinBackoff time.Duration
	MaxBackoff time.Duration
}

// Serve connects to the relay and serves bound streams until parent ends. It
// reconnects with backoff, sending the same ServerInstanceID, until the relay
// refuses the Hello with a failure that is not [Code.Retryable]; it then
// returns that *Error. Before returning it cancels
// every handler's context and waits for the handlers.
func Serve(parent context.Context, cfg ServeConfig) error {
	hello := ServeHello{Version: Version, Credential: cfg.Credential, Resource: cfg.Resource, ServerInstanceID: cfg.ServerInstanceID}
	for _, h := range cfg.Services {
		if h.Serve == nil {
			return errors.New("sandbox link: service handler without Serve")
		}
		hello.Services = append(hello.Services, ServiceVersion{Service: h.Service, Version: h.Version})
	}
	if _, err := Encode(1, hello); err != nil {
		return err
	}
	if cfg.Dial == nil {
		if err := CheckRelayURL(cfg.URL); err != nil {
			return err
		}
	}
	minWait, maxWait := cfg.MinBackoff, cfg.MaxBackoff
	if minWait <= 0 {
		minWait = 100 * time.Millisecond
	}
	if maxWait < minWait {
		maxWait = max(30*time.Second, minWait)
	}
	s := &server{cfg: cfg, dial: dialerFor(cfg.Dial, cfg.URL, cfg.TLS), attachments: map[sandboxwire.ID]*served{},
		closedIDs: map[sandboxwire.ID]time.Time{}}
	ctx, cancel := context.WithCancel(parent)
	stop := func(err error) error {
		cancel()
		s.wg.Wait()
		return err
	}
	wait := minWait
	for {
		connected, err := s.link(ctx, hello)
		if parent.Err() != nil {
			return stop(parent.Err())
		}
		var refused *Error
		if !connected && errors.As(err, &refused) && !refused.Code.Retryable() {
			return stop(refused)
		}
		if cfg.OnDisconnected != nil {
			cfg.OnDisconnected(err)
		}
		if connected {
			wait = minWait
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait/2 + rand.N(wait/2+1)):
		}
		wait = min(2*wait, maxWait)
	}
}

type server struct {
	cfg  ServeConfig
	dial Dialer
	wg   sync.WaitGroup

	mu          sync.Mutex
	attachments map[sandboxwire.ID]*served
	binds       uint64 // bind sequence of the last stream bound
	// closedIDs holds recently closed attachment IDs until the time given. A
	// Bind the relay sent before a close can reach bind after the close.
	closedIDs map[sandboxwire.ID]time.Time
}

// served is one attachment this serve peer has bound, from its first Bind to
// its close.
type served struct {
	id      sandboxwire.ID
	streams int
	lost    bool
	ctx     context.Context
	cancel  context.CancelFunc
}

// link runs one connection. connected reports whether the Hello was accepted.
func (s *server) link(ctx context.Context, hello ServeHello) (connected bool, err error) {
	var seq sandboxwire.RequestSequence
	sess, ctl, err := connect(ctx, s.dial, hello, &seq)
	if err != nil {
		return false, err
	}
	defer sess.Close()
	stop := context.AfterFunc(ctx, func() { sess.Close() })
	defer stop()
	if s.cfg.OnConnected != nil {
		s.cfg.OnConnected()
	}
	done := make(chan error, 1)
	go func() { done <- s.control(ctl) }()
	for {
		st, err := sess.AcceptStream()
		if err != nil {
			sess.Close()
			if cerr := <-done; cerr != nil {
				return true, cerr
			}
			return true, err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.bind(ctx, st)
		}()
	}
}

// control reads relay events until the link ends. The relay sends a serve
// peer nothing but AttachmentClosed; any request, whatever its ID, is a
// ProtocolViolation.
func (s *server) control(ctl *yamux.Stream) error {
	defer ctl.Session().Close()
	for {
		_, m, err := ReadMessage(ctl)
		if err != nil {
			return err
		}
		closed, ok := m.(AttachmentClosed)
		if !ok {
			return Fail(ProtocolViolation)
		}
		s.closed(closed)
	}
}

// bind accepts one relay-opened stream and runs its handler.
func (s *server) bind(ctx context.Context, st *yamux.Stream) {
	st.SetDeadline(time.Now().Add(HandshakeTimeout))
	id, m, err := ReadMessage(st)
	b, ok := m.(Bind)
	var h *ServiceHandler
	if ok {
		for i := range s.cfg.Services {
			if s.cfg.Services[i].Service == b.Service {
				h = &s.cfg.Services[i]
			}
		}
	}
	var refuse Code
	var a *served
	var seq uint64
	switch {
	case err != nil || !ok:
		refuse = ProtocolViolation
	case h == nil:
		refuse = ServiceUnavailable
	case h.Version != b.Version:
		refuse = VersionMismatch
	case b.ExpectedServerInstanceID != s.cfg.ServerInstanceID:
		refuse = InstanceChanged
	default:
		if a, seq = s.track(ctx, b.AttachmentID); a == nil {
			refuse = LeaseExpired
		}
	}
	if refuse != 0 {
		// A frame that could not be read has no request ID to answer.
		if WriteMessage(st, id, FailureFor(OpBind, Fail(refuse))) != nil {
			st.Reset()
			return
		}
		st.Close()
		return
	}
	defer s.release(a)
	if err := WriteMessage(st, id, Bound{}); err != nil {
		st.Reset()
		return
	}
	st.SetDeadline(time.Time{})
	h.Serve(a.ctx, b, seq, st)
}

// track counts a bound stream of attachment id and returns its bind sequence.
// It returns nil for a recently closed attachment.
func (s *server) track(ctx context.Context, id sandboxwire.ID) (*served, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if until, closed := s.closedIDs[id]; closed && time.Now().Before(until) {
		return nil, 0
	}
	a := s.attachments[id]
	if a == nil {
		a = &served{id: id}
		a.ctx, a.cancel = context.WithCancel(ctx)
		s.attachments[id] = a
	}
	a.streams++
	if a.lost {
		a.lost = false
		if s.cfg.OnAttachmentRestored != nil {
			s.cfg.OnAttachmentRestored(id)
		}
	}
	s.binds++
	return a, s.binds
}

// release ends one bound stream of a. Only the current attachment of its ID
// can become lost; a closed one is final.
func (s *server) release(a *served) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.streams--
	if a.streams == 0 && !a.lost && s.attachments[a.id] == a {
		a.lost = true
		if s.cfg.OnAttachmentLost != nil {
			s.cfg.OnAttachmentLost(a.id)
		}
	}
}

// closed ends an attachment and remembers its ID for HandshakeTimeout, which
// bounds how long a Bind the relay sent before the close takes to arrive.
func (s *server) closed(c AttachmentClosed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, until := range s.closedIDs {
		if !now.Before(until) {
			delete(s.closedIDs, id)
		}
	}
	s.closedIDs[c.AttachmentID] = now.Add(HandshakeTimeout)
	a := s.attachments[c.AttachmentID]
	if a == nil {
		return
	}
	delete(s.attachments, c.AttachmentID)
	a.cancel()
	if s.cfg.OnAttachmentClosed != nil {
		s.cfg.OnAttachmentClosed(c.AttachmentID, c.Reason)
	}
}
