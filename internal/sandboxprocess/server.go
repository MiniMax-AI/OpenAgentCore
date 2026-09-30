package sandboxprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// maxInFlight bounds the requests one stream runs concurrently. A request
// beyond it is answered with CodeBusy; reading never pauses, so the end of the
// stream is always seen.
const maxInFlight = 64

// Attachment is the authenticated attachment a stream belongs to. The Link
// layer establishes it; no request payload selects it.
type Attachment struct {
	ID sandboxwire.ID
}

// Conn is the server side of one stream: the requests arriving on it and the
// events a Service sends on it.
type Conn struct {
	att    Attachment
	ctx    context.Context
	cancel context.CancelCauseFunc
	rw     io.ReadWriteCloser

	wmu sync.Mutex

	gmu   sync.Mutex
	gates map[sandboxwire.ID]chan struct{}
}

// Attachment returns the stream's attachment.
func (c *Conn) Attachment() Attachment { return c.att }

// Context is cancelled when the stream ends.
func (c *Conn) Context() context.Context { return c.ctx }

// Send writes an event. It blocks while the peer is not reading, which is the
// stream's backpressure, and while the Start or Attach response that
// subscribed the stream to the operation is still unwritten, so a peer always
// sees that response before the operation's events.
func (c *Conn) Send(ev Event) error {
	id := ev.Header().OperationID
	for {
		c.gmu.Lock()
		gate := c.gates[id]
		c.gmu.Unlock()
		if gate == nil {
			break
		}
		select {
		case <-gate:
		case <-c.ctx.Done():
			return context.Cause(c.ctx)
		}
	}
	return c.write(0, ev)
}

func (c *Conn) write(requestID uint64, m Message) error {
	if err := c.ctx.Err(); err != nil {
		return context.Cause(c.ctx)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	err := sandboxwire.WriteFrame(c.rw, sandboxwire.Frame{Type: m.MessageType(), RequestID: requestID, Payload: Encode(m)})
	if err != nil {
		c.cancel(err)
	}
	return err
}

func (c *Conn) openGate(id sandboxwire.ID) chan struct{} {
	gate := make(chan struct{})
	c.gmu.Lock()
	c.gates[id] = gate
	c.gmu.Unlock()
	return gate
}

func (c *Conn) closeGate(id sandboxwire.ID, gate chan struct{}) {
	c.gmu.Lock()
	if c.gates[id] == gate {
		delete(c.gates, id)
	}
	c.gmu.Unlock()
	close(gate)
}

// ErrProtocol reports a peer that broke the framing or message rules; Serve
// closes the stream.
var ErrProtocol = errors.New("sandboxprocess: protocol violation")

// Serve runs svc on one stream until the stream ends or ctx is done, then
// closes rw and waits for running requests. It returns nil when the peer
// closed the stream or ctx was cancelled.
//
// Up to maxInFlight requests run at once; a request beyond that is answered
// with CodeBusy and EffectNone without running. A malformed request payload is
// answered with CodeInvalidArgument. A malformed frame, an unknown or
// non-request tag, a RequestID that does not increase, or a peer that keeps
// sending while maxInFlight rejections wait to be written ends the stream with
// ErrProtocol, and the request is not dispatched.
func Serve(ctx context.Context, rw io.ReadWriteCloser, att Attachment, svc Service) error {
	ctx, cancel := context.WithCancelCause(ctx)
	c := &Conn{att: att, ctx: ctx, cancel: cancel, rw: rw, gates: map[sandboxwire.ID]chan struct{}{}}
	stop := context.AfterFunc(ctx, func() { rw.Close() })
	defer stop()

	running := make(chan struct{}, maxInFlight)
	rejecting := make(chan struct{}, maxInFlight)
	var seq sandboxwire.RequestSequence
	var wg sync.WaitGroup
	err := func() error {
		for {
			f, err := sandboxwire.ReadFrame(rw, sandboxwire.MaxPayload)
			if err != nil {
				return err
			}
			if kind, err := tags.Classify(f.Type); err != nil || kind != sandboxwire.KindRequest || !seq.Admit(f.RequestID) {
				return fmt.Errorf("%w: frame type %#04x request %d", ErrProtocol, f.Type, f.RequestID)
			}
			select {
			case running <- struct{}{}:
			default:
				select {
				case rejecting <- struct{}{}:
				default:
					return fmt.Errorf("%w: the peer overran %d pending requests", ErrProtocol, 2*maxInFlight)
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					c.write(f.RequestID, ResponseFailure{Request: f.Type, Failure: *Fail(CodeBusy, sandboxwire.EffectNone, "%d requests are running on this stream", maxInFlight)})
					<-rejecting
				}()
				continue
			}
			m, derr := Decode(f.Type, f.Payload)
			var gateID sandboxwire.ID
			var gate chan struct{}
			switch m := m.(type) {
			case StartRequest:
				gateID, gate = m.OperationID, c.openGate(m.OperationID)
			case AttachRequest:
				gateID, gate = m.OperationID, c.openGate(m.OperationID)
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				var resp Message
				if derr != nil {
					resp = ResponseFailure{Request: f.Type, Failure: *Fail(CodeInvalidArgument, sandboxwire.EffectNone, "%v", derr)}
				} else {
					resp = dispatch(ctx, svc, c, m)
				}
				c.write(f.RequestID, resp)
				if gate != nil {
					c.closeGate(gateID, gate)
				}
				<-running
			}()
		}
	}()
	if cause := context.Cause(ctx); cause != nil {
		err = cause // a failed write or the caller's cancellation ended the read
	}
	cancel(err)
	rw.Close()
	wg.Wait()
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func dispatch(ctx context.Context, svc Service, c *Conn, m Message) Message {
	var resp Message
	var err error
	switch m := m.(type) {
	case DescribeRequest:
		resp, err = svc.Describe(ctx, c, m)
	case StartRequest:
		resp, err = svc.Start(ctx, c, m)
	case AttachRequest:
		resp, err = svc.Attach(ctx, c, m)
	case InspectRequest:
		resp, err = svc.Inspect(ctx, c, m)
	case WriteStdinRequest:
		resp, err = svc.WriteStdin(ctx, c, m)
	case CloseStdinRequest:
		resp, err = svc.CloseStdin(ctx, c, m)
	case CloseOutputRequest:
		resp, err = svc.CloseOutput(ctx, c, m)
	case ResizePTYRequest:
		resp, err = svc.ResizePTY(ctx, c, m)
	case SignalRequest:
		resp, err = svc.Signal(ctx, c, m)
	case CancelRequest:
		resp, err = svc.Cancel(ctx, c, m)
	case AckEventsRequest:
		resp, err = svc.AckEvents(ctx, c, m)
	case ReleaseRequest:
		resp, err = svc.Release(ctx, c, m)
	}
	if err == nil {
		return resp
	}
	var f *Failure
	if !errors.As(err, &f) || !f.Code.Valid() || !f.Effect.Valid() {
		f = Fail(CodeUnknown, sandboxwire.EffectPossible, "%v", err)
	}
	return ResponseFailure{Request: m.MessageType(), Failure: *f}
}
