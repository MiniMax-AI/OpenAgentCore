package sandboxfs

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// MaxInFlight is how many requests one stream holds at once, from admission
// until the response is written. The server answers a request beyond it with
// ResourceExhausted and EffectNone, so it keeps reading and a CancelRequest
// always gets through while the client reads responses.
const MaxInFlight = 256

// ErrSuperseded ends a stream that a successor stream of the same attachment
// replaced.
var ErrSuperseded = errors.New("sandboxfs: stream superseded by a successor")

// Server answers File streams for one Service. It admits one stream at a time
// for each (ServerInstanceID, AttachmentID), in the order Link bound them: a
// stream bound before one already admitted is refused, and a successor runs no
// request until every earlier stream that ran one has stopped admitting
// requests and every request it admitted has finished. A successor that has
// run nothing ends at once when it is superseded or its stream or context
// ends. Its methods are safe for concurrent use.
type Server struct {
	svc Service

	mu sync.Mutex
	// streams holds the newest stream of each attachment, kept after it ends
	// until the attachment's lease has ended and the stream has settled.
	streams map[streamKey]*stream

	awaitPredecessor func() // test seam: runs when a request of a successor starts waiting
}

type streamKey struct{ instance, attachment sandboxwire.ID }

// NewServer returns a Server for svc. A file service uses one Server for all
// of its streams, because the succession fence spans them.
func NewServer(svc Service) *Server {
	return &Server{svc: svc, streams: map[streamKey]*stream{}}
}

// Serve answers the requests on conn until the stream ends, ctx is done or a
// successor stream of the same attachment supersedes it. a is the attachment
// Link authenticated for the stream; Serve refuses one without an ID, server
// instance, lease or valid export grants. seq is the stream's Link bind
// sequence: Serve refuses a stream bound before one of its attachment it
// already admitted with ErrSuperseded, without running anything. Serve
// owns conn and closes it. On return every request context is cancelled and
// every handler has finished. A stream that ends cleanly returns nil; a
// superseded one returns ErrSuperseded.
func (s *Server) Serve(ctx context.Context, conn io.ReadWriteCloser, a Attachment, seq uint64) error {
	if err := a.validate(); err != nil {
		conn.Close()
		return err
	}
	a.Exports = slices.Clone(a.Exports)
	ctx, cancel := context.WithCancel(ctx)
	st := &stream{srv: s, key: streamKey{a.ServerInstanceID, a.ID}, conn: conn, svc: s.svc, a: a, bindSeq: seq, cancel: cancel,
		drained: make(chan struct{}), inflight: map[uint64]context.CancelFunc{}, acquiring: map[HandleID]chan struct{}{}}
	prev, ok := s.admit(st)
	if !ok {
		cancel()
		conn.Close()
		return ErrSuperseded
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer func() {
		cancel()
		stop()
		conn.Close()
		st.wg.Wait()
		close(st.drained)
	}()
	if prev != nil {
		prev.fence()
	}
	for {
		f, err := sandboxwire.ReadFrame(conn, sandboxwire.MaxPayload)
		if err == nil {
			err = st.dispatch(ctx, f)
		}
		switch {
		case err == nil:
		case st.superseded():
			return ErrSuperseded
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, io.EOF):
			return nil
		default:
			return err
		}
	}
}

// admit makes st the newest stream of its attachment and returns the
// predecessor it must fence. It refuses st when a stream bound later, or the
// same stream, was admitted already. st waits for the predecessor to drain
// when the predecessor has run a request; otherwise the predecessor never will,
// and st waits for what the predecessor was waiting for. It forgets each
// attachment whose lease has ended and whose newest stream has settled.
func (s *Server) admit(st *stream) (prev *stream, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.streams {
		if closed(e.a.Lease.Done()) && e.settled() {
			delete(s.streams, k)
		}
	}
	prev = s.streams[st.key]
	if prev != nil && prev.bindSeq >= st.bindSeq {
		return nil, false
	}
	if prev != nil {
		st.after = prev.after
		if prev.started.Load() {
			st.after = prev.drained
		}
	}
	s.streams[st.key] = st
	return prev, true
}

// start reports whether st may run a request: once every earlier stream that
// ran one has drained, and only while no successor has superseded st. It
// reports false when st was superseded or ctx ended first; st has then run
// nothing. The check and the mark are one step under the Server's lock, so a
// successor either waits for st or knows that st will run nothing.
func (st *stream) start(ctx context.Context) bool {
	if st.started.Load() {
		return true
	}
	if st.after != nil {
		if st.srv.awaitPredecessor != nil {
			st.srv.awaitPredecessor()
		}
		// No deadline: a handler an earlier stream admitted may still change
		// state this stream's requests depend on.
		select {
		case <-st.after:
		case <-ctx.Done():
			return false
		}
	}
	st.srv.mu.Lock()
	defer st.srv.mu.Unlock()
	if st.srv.streams[st.key] != st || ctx.Err() != nil {
		return false
	}
	st.started.Store(true)
	return true
}

// settled reports whether st has drained and so has every stream admitted
// before it that ran a request. Until then the attachment's entry carries the
// fence a later stream must wait behind.
func (st *stream) settled() bool {
	return closed(st.drained) && (st.after == nil || closed(st.after))
}

func closed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// stream is one served File stream.
type stream struct {
	srv     *Server
	key     streamKey
	conn    io.ReadWriteCloser
	svc     Service
	a       Attachment
	bindSeq uint64
	cancel  context.CancelFunc
	drained chan struct{}               // closed once the stream has ended and every handler finished
	after   <-chan struct{}             // closed once every earlier stream that ran a request has drained; nil when none did
	started atomic.Bool                 // a request passed the fence; set under the Server's lock
	seq     sandboxwire.RequestSequence // read loop only
	wmu     sync.Mutex
	wg      sync.WaitGroup

	mu        sync.Mutex
	fenced    bool
	inflight  map[uint64]context.CancelFunc // running handlers, for CancelRequest
	held      int                           // admitted requests whose response is not yet written
	acquiring map[HandleID]chan struct{}    // handle IDs of running acquisitions, closed when each finishes
}

// fence stops admission on the stream, cancels its requests and closes it, so
// its later responses are discarded.
func (st *stream) fence() {
	st.mu.Lock()
	st.fenced = true
	st.mu.Unlock()
	st.cancel()
	st.conn.Close()
}

func (st *stream) superseded() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.fenced
}

// dispatch starts one request. A frame that is not a request, or whose
// RequestID does not increase, ends the stream.
func (st *stream) dispatch(ctx context.Context, f sandboxwire.Frame) error {
	kind, err := tags.Classify(f.Type)
	if err != nil {
		return err
	}
	if kind != sandboxwire.KindRequest {
		return malformed("message type %#04x from the client", f.Type)
	}
	if !st.seq.Admit(f.RequestID) {
		return malformed("request ID %d does not increase", f.RequestID)
	}
	op := Op(f.Type)
	req, err := decodeRequest(op, f.Payload)
	st.mu.Lock()
	if st.fenced || ctx.Err() != nil {
		st.mu.Unlock()
		return context.Canceled
	}
	switch {
	case err != nil:
		st.mu.Unlock()
		return st.reply(f.RequestID, op, nil, NewFailure(CodeInvalidArgument, sandboxwire.EffectNone, err.Error()))
	case op == OpCancelRequest:
		if cancel := st.inflight[req.(*CancelRequestRequest).Target]; cancel != nil {
			cancel()
		}
		st.mu.Unlock()
		return st.reply(f.RequestID, op, &CancelRequestResponse{}, nil)
	case st.held >= MaxInFlight:
		st.mu.Unlock()
		return st.reply(f.RequestID, op, nil, NewFailure(CodeResourceExhausted, sandboxwire.EffectNone, "too many requests in flight"))
	}
	// An acquisition owns its handle ID until it finishes; a release of that
	// ID waits for it, so it releases whatever the acquisition produced.
	var acquired, wait chan struct{}
	if id, ok := acquires(req); ok {
		if st.acquiring[id] != nil {
			st.mu.Unlock()
			return st.reply(f.RequestID, op, nil, NewFailure(CodeInvalidArgument, sandboxwire.EffectNone, "handle ID is already in use"))
		}
		acquired = make(chan struct{})
		st.acquiring[id] = acquired
	} else if id, ok := releases(req); ok {
		wait = st.acquiring[id]
	}
	rctx, cancel := context.WithCancel(ctx)
	st.inflight[f.RequestID] = cancel
	st.held++
	st.mu.Unlock()
	st.wg.Add(1)
	go func() {
		defer st.wg.Done()
		resp, err := st.serve(rctx, op, req, wait)
		st.mu.Lock()
		delete(st.inflight, f.RequestID)
		if acquired != nil {
			id, _ := acquires(req)
			delete(st.acquiring, id)
			close(acquired)
		}
		st.mu.Unlock()
		cancel()
		var fail *Failure
		if err != nil && !errors.As(err, &fail) {
			fail = NewFailure(CodeUnknown, sandboxwire.EffectPossible, err.Error())
		}
		// The request keeps its slot until its response is written, so a
		// client that stops reading stops admission instead of piling up
		// finished requests.
		werr := st.reply(f.RequestID, op, resp, fail)
		st.mu.Lock()
		st.held--
		st.mu.Unlock()
		if werr != nil {
			st.conn.Close()
		}
	}()
	return nil
}

// serve runs one request once the stream may run requests, and after the
// acquisition it must follow, if any.
func (st *stream) serve(ctx context.Context, op Op, req Request, after <-chan struct{}) (message, error) {
	if !st.start(ctx) {
		return nil, NewFailure(CodeCancelled, sandboxwire.EffectNone, "stream superseded or ended before the request ran")
	}
	if after != nil {
		select {
		case <-after:
		case <-ctx.Done():
			return nil, contextFailure(ctx.Err(), sandboxwire.EffectNone)
		}
	}
	return opSpecs[op].serve(ctx, st.svc, st.a, req)
}

// reply writes a response. A response the service built wrongly becomes
// Unknown with EffectPossible, since the request may have run. A fenced
// stream's responses are discarded.
func (st *stream) reply(id uint64, op Op, resp message, fail *Failure) error {
	payload, err := encodeResponse(resp, fail)
	if err != nil {
		payload, _ = encodeResponse(nil, NewFailure(CodeUnknown, sandboxwire.EffectPossible, "service response: "+err.Error()))
	}
	st.wmu.Lock()
	defer st.wmu.Unlock()
	if st.superseded() {
		return nil
	}
	return sandboxwire.WriteFrame(st.conn, sandboxwire.Frame{Type: sandboxwire.ResponseType(uint16(op)), RequestID: id, Payload: payload})
}
