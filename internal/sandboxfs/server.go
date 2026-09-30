package sandboxfs

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// MaxInFlight is how many requests one stream holds at once, from admission
// until the response is written. The server answers a request beyond it with
// ResourceExhausted and EffectNone, so it keeps reading and a CancelRequest
// always gets through while the client reads responses.
const MaxInFlight = 256

// Serve answers the requests on conn with svc until the stream ends or ctx is
// done. a is the attachment Link authenticated for the stream; Serve refuses
// one without an ID, server instance, lease or valid export grants. Serve owns
// conn and closes it. On return every request context is cancelled and every
// handler has finished. A stream that ends cleanly returns nil.
func Serve(ctx context.Context, conn io.ReadWriteCloser, svc Service, a Attachment) error {
	if err := a.validate(); err != nil {
		conn.Close()
		return err
	}
	a.Exports = slices.Clone(a.Exports)
	ctx, cancel := context.WithCancel(ctx)
	s := &server{conn: conn, svc: svc, a: a, inflight: map[uint64]context.CancelFunc{}}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer func() {
		cancel()
		stop()
		conn.Close()
		s.wg.Wait()
	}()
	for {
		f, err := sandboxwire.ReadFrame(conn, sandboxwire.MaxPayload)
		if err == nil {
			err = s.dispatch(ctx, f)
		}
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, io.EOF):
			return nil
		default:
			return err
		}
	}
}

type server struct {
	conn io.ReadWriteCloser
	svc  Service
	a    Attachment
	seq  sandboxwire.RequestSequence // read loop only
	wmu  sync.Mutex
	wg   sync.WaitGroup

	mu       sync.Mutex
	inflight map[uint64]context.CancelFunc // running handlers, for CancelRequest
	held     int                           // admitted requests whose response is not yet written
}

// dispatch starts one request. A frame that is not a request, or whose
// RequestID does not increase, ends the stream.
func (s *server) dispatch(ctx context.Context, f sandboxwire.Frame) error {
	kind, err := tags.Classify(f.Type)
	if err != nil {
		return err
	}
	if kind != sandboxwire.KindRequest {
		return malformed("message type %#04x from the client", f.Type)
	}
	if !s.seq.Admit(f.RequestID) {
		return malformed("request ID %d does not increase", f.RequestID)
	}
	op := Op(f.Type)
	req, err := decodeRequest(op, f.Payload)
	s.mu.Lock()
	switch {
	case err != nil:
		s.mu.Unlock()
		return s.reply(f.RequestID, op, nil, NewFailure(CodeInvalidArgument, sandboxwire.EffectNone, err.Error()))
	case op == OpCancelRequest:
		if cancel := s.inflight[req.(*CancelRequestRequest).Target]; cancel != nil {
			cancel()
		}
		s.mu.Unlock()
		return s.reply(f.RequestID, op, &CancelRequestResponse{}, nil)
	case s.held >= MaxInFlight:
		s.mu.Unlock()
		return s.reply(f.RequestID, op, nil, NewFailure(CodeResourceExhausted, sandboxwire.EffectNone, "too many requests in flight"))
	}
	rctx, cancel := context.WithCancel(ctx)
	s.inflight[f.RequestID] = cancel
	s.held++
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		resp, err := opSpecs[op].serve(rctx, s.svc, s.a, req)
		s.mu.Lock()
		delete(s.inflight, f.RequestID)
		s.mu.Unlock()
		cancel()
		var fail *Failure
		if err != nil && !errors.As(err, &fail) {
			fail = NewFailure(CodeUnknown, sandboxwire.EffectPossible, err.Error())
		}
		// The request keeps its slot until its response is written, so a
		// client that stops reading stops admission instead of piling up
		// finished requests.
		werr := s.reply(f.RequestID, op, resp, fail)
		s.mu.Lock()
		s.held--
		s.mu.Unlock()
		if werr != nil {
			s.conn.Close()
		}
	}()
	return nil
}

// reply writes a response. A response the service built wrongly becomes
// Unknown with EffectPossible, since the request may have run.
func (s *server) reply(id uint64, op Op, resp message, fail *Failure) error {
	payload, err := encodeResponse(resp, fail)
	if err != nil {
		payload, _ = encodeResponse(nil, NewFailure(CodeUnknown, sandboxwire.EffectPossible, "service response: "+err.Error()))
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return sandboxwire.WriteFrame(s.conn, sandboxwire.Frame{Type: sandboxwire.ResponseType(uint16(op)), RequestID: id, Payload: payload})
}
