package sandboxnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// bufferSize is the copy buffer of each splice direction.
const bufferSize = 32 << 10

// Serve handles one Network stream for svc. It reads the stream's one Connect,
// checks the port, the host and every resolved address against egress, dials
// the first permitted address within the Connect's timeout and answers. After
// Connected it splices the stream and the connection: an end of either side's
// writing reaches the other as an orderly half-close after every byte before
// it, and any error, or the end of ctx, aborts both. No idle timeout applies.
//
// ctx is the attachment's. Serve owns s and returns when it is done with it:
// nil after both directions ended in order, ErrProtocolViolation when the
// attacher broke the protocol, the *Error it answered, or the error that
// aborted the stream.
func Serve(ctx context.Context, s sandboxlink.Stream, egress []sandboxlink.EgressRule, svc Service) error {
	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	x := &splice{s: s, cancel: cancel, settled: make(chan struct{})}
	stop := context.AfterFunc(ctx, func() { x.abort(ctx.Err()) })
	defer stop()

	f, err := sandboxwire.ReadFrame(s, MaxMessageBytes)
	if err != nil {
		s.Reset()
		if errors.Is(err, sandboxwire.ErrMalformed) {
			return fmt.Errorf("%w: %w", ErrProtocolViolation, err)
		}
		return err
	}
	var seq sandboxwire.RequestSequence
	if kind, err := tags.Classify(f.Type); err != nil || kind != sandboxwire.KindRequest || !seq.Admit(f.RequestID) {
		s.Reset()
		return fmt.Errorf("%w: frame type %#04x request ID %d", ErrProtocolViolation, f.Type, f.RequestID)
	}
	m, err := Decode(f)
	if err != nil {
		failure := &Error{Code: CodeInvalidArgument, Effect: sandboxwire.EffectNone, Cause: err}
		answerFailure(s, f.RequestID, failure)
		return failure
	}
	req := m.(ConnectRequest)

	var wg sync.WaitGroup
	wg.Go(x.pumpIn)
	defer wg.Wait()
	dialCtx, cancelTimeout := context.WithTimeout(dialCtx, time.Duration(req.TimeoutMillis)*time.Millisecond)
	defer cancelTimeout()
	conn, failure := connect(dialCtx, req, egress, svc)
	if failure != nil {
		if x.advance(dialing, answered, nil) {
			answerFailure(s, f.RequestID, failure)
			return failure
		}
		return x.result()
	}
	if !x.advance(dialing, connecting, conn) {
		conn.SetLinger(0)
		conn.Close()
		return x.result()
	}
	if err := WriteMessage(s, f.RequestID, ConnectResponse{Result: ResultConnected}); err != nil {
		x.abort(err)
		return x.result()
	}
	if !x.advance(connecting, connected, nil) {
		return x.result()
	}
	_, err = io.CopyBuffer(struct{ io.Writer }{s}, struct{ io.Reader }{conn}, make([]byte, bufferSize))
	if err == nil {
		err = s.CloseWrite()
	}
	if err != nil {
		x.abort(err)
	}
	wg.Wait()
	if err := x.result(); err != nil {
		return err
	}
	conn.Close()
	return s.Close()
}

// answerFailure writes a failure answer and ends the stream in order, so the
// attacher reads the answer before the end.
func answerFailure(s sandboxlink.Stream, requestID uint64, e *Error) {
	if WriteMessage(s, requestID, ConnectResponse{Result: ResultFailed, Code: e.Code, Effect: e.Effect}) != nil {
		s.Reset()
		return
	}
	s.Close()
}

// connect resolves and checks the destination and dials it. Every failure is
// an *Error to answer.
func connect(dialCtx context.Context, req ConnectRequest, egress []sandboxlink.EgressRule, svc Service) (*net.TCPConn, *Error) {
	if !permitsPort(egress, req.Port) {
		return nil, &Error{Code: CodeDenied, Effect: sandboxwire.EffectNone}
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(req.Host); err == nil {
		addrs = []netip.Addr{a}
	} else if addrs, err = svc.Resolve(dialCtx, req.Host); err != nil {
		return nil, outcome(dialCtx, err, CodeNameResolutionFailed, sandboxwire.EffectNone)
	} else if len(addrs) == 0 {
		return nil, &Error{Code: CodeNameNotResolved, Effect: sandboxwire.EffectNone}
	}
	for _, a := range addrs {
		target := netip.AddrPortFrom(a.Unmap(), req.Port)
		if !permits(egress, target) {
			continue
		}
		conn, err := svc.Dial(dialCtx, target)
		if err != nil {
			return nil, outcome(dialCtx, err, CodeIO, sandboxwire.EffectPossible)
		}
		return conn, nil
	}
	return nil, &Error{Code: CodeDenied, Effect: sandboxwire.EffectNone}
}

// outcome types a Service error: a valid *Error stands; otherwise a passed
// deadline or the end of dialCtx decides, and any other error becomes code.
// The deadline is read from the clock, because a socket deadline set from it
// can fire before dialCtx reports its end.
func outcome(dialCtx context.Context, err error, code Code, effect sandboxwire.Effect) *Error {
	var e *Error
	deadline, ok := dialCtx.Deadline()
	switch {
	case errors.As(err, &e) && e.Code.Valid() && e.Effect.Valid():
		return e
	case ok && !time.Now().Before(deadline):
		return &Error{Code: CodeTimedOut, Effect: effect, Cause: err}
	case dialCtx.Err() != nil:
		return &Error{Code: CodeCancelled, Effect: effect, Cause: err}
	}
	return &Error{Code: code, Effect: effect, Cause: err}
}

// phase is where a splice stands. It moves from dialing to answered, or
// through connecting to connected, and to aborted from any phase.
type phase uint8

const (
	dialing    phase = iota
	connecting       // the dial succeeded and Connected is being written
	connected        // Connected was written
	answered         // a failure is being or was answered
	aborted
)

// splice holds the state the two directions share. pumpIn reads the stream
// from the start, so it sees bytes or a second Connect that arrive before
// Connected.
type splice struct {
	s       sandboxlink.Stream
	cancel  context.CancelFunc // cancels the dial
	settled chan struct{}      // closed when the phase becomes connected, answered or aborted
	mu      sync.Mutex
	phase   phase
	conn    *net.TCPConn
	err     error
}

// advance moves the splice from one phase to the next, recording conn when it
// is not nil. It fails when the splice is no longer in from, which only an
// abort causes.
func (x *splice) advance(from, to phase, conn *net.TCPConn) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.phase != from {
		return false
	}
	x.phase = to
	if conn != nil {
		x.conn = conn
	}
	if to != connecting {
		close(x.settled)
	}
	return true
}

func (x *splice) state() (phase, *net.TCPConn) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.phase, x.conn
}

func (x *splice) result() error {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.err
}

// abort resets the stream and the connection, and cancels a dial in progress.
// The first cause is kept.
func (x *splice) abort(cause error) {
	x.mu.Lock()
	if x.phase == aborted {
		x.mu.Unlock()
		return
	}
	if x.phase == dialing || x.phase == connecting {
		close(x.settled)
	}
	x.phase, x.err = aborted, cause
	conn := x.conn
	x.mu.Unlock()
	x.cancel()
	x.s.Reset()
	if conn != nil {
		conn.SetLinger(0)
		conn.Close()
	}
}

// pumpIn copies the stream to the connection. Bytes while dialing break the
// protocol; bytes after a successful dial wait until Connected is written, so
// nothing reaches the destination before the answer is out. The stream's
// orderly end becomes the connection's CloseWrite once Connected is written.
func (x *splice) pumpIn() {
	buf := make([]byte, bufferSize)
	for {
		n, err := x.s.Read(buf)
		if n > 0 {
			if p, _ := x.state(); p == dialing {
				x.abort(fmt.Errorf("%w: bytes before the dial succeeded", ErrProtocolViolation))
				return
			}
			<-x.settled
			p, conn := x.state()
			if p != connected {
				return
			}
			if _, werr := conn.Write(buf[:n]); werr != nil {
				x.abort(werr)
				return
			}
		}
		switch {
		case err == io.EOF:
			<-x.settled
			if p, conn := x.state(); p == connected {
				if err := conn.CloseWrite(); err != nil {
					x.abort(err)
				}
			}
			return
		case err != nil:
			// After a failure answer the stream is closed and reading it fails;
			// resetting it then would discard the answer.
			if p, _ := x.state(); p != answered {
				x.abort(err)
			}
			return
		}
	}
}
