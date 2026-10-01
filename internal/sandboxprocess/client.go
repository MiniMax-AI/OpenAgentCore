package sandboxprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Client is the caller side of one stream.
type Client struct {
	rw io.ReadWriteCloser
	// writing holds the right to write a frame; waiting for it honors the
	// caller's context.
	writing chan struct{}

	// seq allocates request IDs; it is used only while holding writing.
	seq sandboxwire.RequestSequence

	mu sync.Mutex
	// issued is the last request ID allocated, so a response to an ID never
	// sent is told from one whose caller gave up.
	issued  uint64
	pending map[uint64]*call
	ops     map[sandboxwire.ID]*Operation
	caps    *Capabilities
	err     error
	done    chan struct{}

	afterWrite func() // test seam: runs once a frame is recorded as written
}

type call struct {
	request uint16
	resp    chan Message
	// onResponse runs in the read loop before any later frame is read, so a
	// handle is subscribed before the operation's first event arrives.
	onResponse func(Message)
}

// ErrClientClosed is the cause after Close.
var ErrClientClosed = errors.New("sandboxprocess: client closed")

// NewClient starts reading rw. Close, or the end of rw, stops the client.
func NewClient(rw io.ReadWriteCloser) *Client {
	c := &Client{rw: rw, writing: make(chan struct{}, 1), pending: map[uint64]*call{}, ops: map[sandboxwire.ID]*Operation{}, done: make(chan struct{})}
	go c.readLoop()
	return c
}

// Close ends the stream. Operations keep running on the service.
func (c *Client) Close() error {
	c.fail(ErrClientClosed)
	<-c.done
	return nil
}

// Done is closed when the stream has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns why the stream ended.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
	c.rw.Close()
}

func (c *Client) readLoop() {
	defer func() {
		c.mu.Lock()
		ops := c.ops
		c.ops = map[sandboxwire.ID]*Operation{}
		c.mu.Unlock()
		for _, op := range ops {
			op.finish(false)
		}
		close(c.done)
	}()
	for {
		f, err := sandboxwire.ReadFrame(c.rw, sandboxwire.MaxPayload)
		if err == nil {
			err = c.handle(f)
		}
		if err != nil {
			c.fail(err)
			return
		}
	}
}

func (c *Client) handle(f sandboxwire.Frame) error {
	m, err := Decode(f.Type, f.Payload)
	if err != nil {
		return err
	}
	if ev, ok := m.(Event); ok {
		if f.RequestID != 0 {
			return fmt.Errorf("%w: event with request %d", ErrProtocol, f.RequestID)
		}
		c.mu.Lock()
		op := c.ops[ev.Header().OperationID]
		c.mu.Unlock()
		if op != nil {
			op.deliver(ev)
		}
		return nil
	}
	if sandboxwire.IsResponse(f.Type) {
		c.mu.Lock()
		cl, sent := c.pending[f.RequestID], f.RequestID != 0 && f.RequestID <= c.issued
		delete(c.pending, f.RequestID)
		c.mu.Unlock()
		if !sent {
			return fmt.Errorf("%w: response to unknown request %d", ErrProtocol, f.RequestID)
		}
		if cl == nil {
			return nil // its caller gave up waiting
		}
		if f.Type != sandboxwire.ResponseType(cl.request) {
			return fmt.Errorf("%w: response %#04x to request %#04x", ErrProtocol, f.Type, cl.request)
		}
		if cl.onResponse != nil {
			cl.onResponse(m)
		}
		cl.resp <- m
		return nil
	}
	return fmt.Errorf("%w: request frame %#04x from server", ErrProtocol, f.Type)
}

// do sends req and waits for its response. A failure is returned as *Failure.
// When ctx ends before the request is written, the failure has EffectNone.
// When it ends while the frame is being written, the frame may be partial, so
// the stream is closed and the failure has EffectPossible. When it ends after
// the frame is written, the stream stays open and the failure has
// EffectPossible.
func (c *Client) do(ctx context.Context, req Message, onResponse func(Message)) (Message, error) {
	select {
	case c.writing <- struct{}{}:
	case <-ctx.Done():
		return nil, contextFailure(ctx, sandboxwire.EffectNone)
	case <-c.done:
		return nil, Fail(CodeIO, sandboxwire.EffectNone, "stream ended: %v", c.Err())
	}
	if ctx.Err() != nil {
		<-c.writing
		return nil, contextFailure(ctx, sandboxwire.EffectNone)
	}
	// The ID is allocated while holding the right to write, so IDs increase
	// in wire order as the request ID rule requires.
	cl := &call{request: req.MessageType(), resp: make(chan Message, 1), onResponse: onResponse}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		<-c.writing
		return nil, Fail(CodeIO, sandboxwire.EffectNone, "stream ended: %v", c.err)
	}
	id := c.seq.Next()
	c.issued = id
	c.pending[id] = cl
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	// Whichever of the write and the cancellation finishes first decides: only
	// a cancellation that interrupts the write closes the stream. Any callback
	// already running settles before the right to write is released.
	const writing, written, interrupted = 0, 1, 2
	var state atomic.Int32
	settled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(settled)
		if state.CompareAndSwap(writing, interrupted) {
			c.fail(fmt.Errorf("sandboxprocess: request %d interrupted while being written: %w", id, context.Cause(ctx)))
		}
	})
	err := sandboxwire.WriteFrame(c.rw, sandboxwire.Frame{Type: req.MessageType(), RequestID: id, Payload: Encode(req)})
	state.CompareAndSwap(writing, written)
	if c.afterWrite != nil {
		c.afterWrite()
	}
	if !stop() {
		<-settled
	}
	<-c.writing
	if state.Load() == interrupted {
		forget()
		return nil, contextFailure(ctx, sandboxwire.EffectPossible)
	}
	if err != nil {
		c.fail(err)
	}

	var m Message
	select {
	case m = <-cl.resp:
	case <-ctx.Done():
	case <-c.done:
	}
	if m == nil {
		// A response that arrived with the cancellation still counts.
		select {
		case m = <-cl.resp:
		default:
			forget()
			if ctx.Err() != nil {
				return nil, contextFailure(ctx, sandboxwire.EffectPossible)
			}
			return nil, Fail(CodeIO, sandboxwire.EffectPossible, "stream ended: %v", c.Err())
		}
	}
	if rf, ok := m.(ResponseFailure); ok {
		f := rf.Failure
		return nil, &f
	}
	return m, nil
}

func contextFailure(ctx context.Context, effect sandboxwire.Effect) *Failure {
	code := CodeCancelled
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = CodeDeadlineExceeded
	}
	return Fail(code, effect, "%v", ctx.Err())
}

// Describe returns the service incarnation and capabilities. The client keeps
// the capabilities; WriteStdin chunks by their MaxDataBytes.
func (c *Client) Describe(ctx context.Context) (DescribeResponse, error) {
	m, err := c.do(ctx, DescribeRequest{}, nil)
	if err != nil {
		return DescribeResponse{}, err
	}
	r := m.(DescribeResponse)
	c.mu.Lock()
	c.caps = &r.Capabilities
	c.mu.Unlock()
	return r, nil
}

// capabilities returns the kept capabilities, describing the service first
// when the client has not.
func (c *Client) capabilities(ctx context.Context) (Capabilities, error) {
	c.mu.Lock()
	caps := c.caps
	c.mu.Unlock()
	if caps != nil {
		return *caps, nil
	}
	r, err := c.Describe(ctx)
	return r.Capabilities, err
}

// Start starts the operation, or finds the existing one with the same ID and
// spec. Either way the handle observes events from sequence 1; for an existing
// operation whose early events were acknowledged, Start fails with
// CodeReplayGap and Attach resumes from a later sequence.
func (c *Client) Start(ctx context.Context, instance, id sandboxwire.ID, spec ProcessSpec) (*Operation, StartDisposition, error) {
	op, err := c.register(OperationRef{ServerInstanceID: instance, OperationID: id})
	if err != nil {
		return nil, 0, err
	}
	m, err := c.do(ctx, StartRequest{OperationRef: op.ref, Spec: spec}, func(m Message) {
		if r, ok := m.(StartResponse); ok && r.Disposition == StartCreated {
			op.activate(0)
		}
	})
	if err != nil {
		c.unregister(op)
		return nil, 0, err
	}
	disp := m.(StartResponse).Disposition
	if disp == StartExisting {
		if _, err := op.attach(ctx, 0); err != nil {
			c.unregister(op)
			return nil, disp, err
		}
	}
	return op, disp, nil
}

// Attach observes an existing operation from the event after afterSequence.
func (c *Client) Attach(ctx context.Context, instance, id sandboxwire.ID, afterSequence uint64) (*Operation, OperationStatus, error) {
	op, err := c.register(OperationRef{ServerInstanceID: instance, OperationID: id})
	if err != nil {
		return nil, OperationStatus{}, err
	}
	st, err := op.attach(ctx, afterSequence)
	if err != nil {
		c.unregister(op)
		return nil, OperationStatus{}, err
	}
	return op, st, nil
}

func (c *Client) register(ref OperationRef) (*Operation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, Fail(CodeIO, sandboxwire.EffectNone, "stream ended: %v", c.err)
	}
	if c.ops[ref.OperationID] != nil {
		return nil, Fail(CodeInvalidArgument, sandboxwire.EffectNone, "operation %s already has a handle on this stream", ref.OperationID)
	}
	op := &Operation{c: c, ref: ref, events: make(chan Event), wake: make(chan struct{}, 1), stop: make(chan struct{}), stdin: make(chan struct{}, 1)}
	c.ops[ref.OperationID] = op
	go op.forward()
	return op, nil
}

func (c *Client) unregister(op *Operation) {
	c.mu.Lock()
	if c.ops[op.ref.OperationID] == op {
		delete(c.ops, op.ref.OperationID)
	}
	c.mu.Unlock()
	op.finish(true)
}

// Operation is a handle to one operation observed on a Client's stream.
type Operation struct {
	c   *Client
	ref OperationRef

	events chan Event
	wake   chan struct{}
	stop   chan struct{}

	mu       sync.Mutex
	active   bool
	next     uint64
	queue    []Event
	finished bool
	stopped  bool

	// stdin is held while the stdin offset is used or refreshed; waiting for
	// it honors the caller's context. The offset changes only under it.
	stdin       chan struct{}
	stdinOffset atomic.Uint64
}

// Ref returns the operation's address.
func (op *Operation) Ref() OperationRef { return op.ref }

// Events delivers the operation's events in sequence order, each once. It is
// closed after Release, Detach or the end of the stream; the queue it drains
// holds at most the service's unacknowledged replay window.
func (op *Operation) Events() <-chan Event { return op.events }

// StdinOffset is the next stdin offset WriteStdin uses.
func (op *Operation) StdinOffset() uint64 { return op.stdinOffset.Load() }

// lockStdin takes the stdin offset until the returned release, or fails with
// EffectNone when ctx or the stream ends first.
func (op *Operation) lockStdin(ctx context.Context) (release func(), err error) {
	select {
	case op.stdin <- struct{}{}:
		return func() { <-op.stdin }, nil
	case <-ctx.Done():
		return nil, contextFailure(ctx, sandboxwire.EffectNone)
	case <-op.c.done:
		return nil, Fail(CodeIO, sandboxwire.EffectNone, "stream ended: %v", op.c.Err())
	}
}

func (op *Operation) activate(after uint64) {
	op.mu.Lock()
	op.active, op.next = true, after+1
	op.mu.Unlock()
}

// deliver queues ev if it is the next expected event. Events from an earlier
// subscription on this stream repeat or skip sequences and are dropped.
func (op *Operation) deliver(ev Event) {
	op.mu.Lock()
	ok := op.active && !op.finished && ev.Header().Sequence == op.next
	if ok {
		op.next++
		op.queue = append(op.queue, ev)
	}
	op.mu.Unlock()
	if ok {
		op.signal()
	}
}

func (op *Operation) signal() {
	select {
	case op.wake <- struct{}{}:
	default:
	}
}

// finish stops delivery. With discard it drops queued events; otherwise they
// drain before Events closes.
func (op *Operation) finish(discard bool) {
	op.mu.Lock()
	op.finished = true
	if discard && !op.stopped {
		op.stopped = true
		close(op.stop)
	}
	op.mu.Unlock()
	op.signal()
}

func (op *Operation) forward() {
	defer close(op.events)
	for {
		op.mu.Lock()
		var ev Event
		if len(op.queue) > 0 {
			ev = op.queue[0]
			op.queue[0] = nil
			op.queue = op.queue[1:]
		}
		finished := op.finished
		op.mu.Unlock()
		if ev == nil {
			if finished {
				return
			}
			select {
			case <-op.wake:
			case <-op.stop:
				return
			}
			continue
		}
		select {
		case op.events <- ev:
		case <-op.stop:
			return
		}
	}
}

func (op *Operation) attach(ctx context.Context, after uint64) (OperationStatus, error) {
	release, err := op.lockStdin(ctx)
	if err != nil {
		return OperationStatus{}, err
	}
	defer release()
	m, err := op.c.do(ctx, AttachRequest{OperationRef: op.ref, AfterSequence: after}, func(m Message) {
		if _, ok := m.(AttachResponse); ok {
			op.activate(after)
		}
	})
	if err != nil {
		return OperationStatus{}, err
	}
	st := m.(AttachResponse).Status
	op.stdinOffset.Store(st.StdinOffset)
	return st, nil
}

// Inspect returns the operation's status and refreshes the stdin offset. It
// waits for a WriteStdin or CloseStdin in progress, so the offset it stores is
// never older than theirs.
func (op *Operation) Inspect(ctx context.Context) (OperationStatus, error) {
	release, err := op.lockStdin(ctx)
	if err != nil {
		return OperationStatus{}, err
	}
	defer release()
	m, err := op.c.do(ctx, InspectRequest{op.ref}, nil)
	if err != nil {
		return OperationStatus{}, err
	}
	st := m.(InspectResponse).Status
	op.stdinOffset.Store(st.StdinOffset)
	return st, nil
}

// WriteStdin writes data at the tracked offset, in chunks of the service's
// MaxDataBytes, advancing the offset by each accepted count. It returns the
// bytes accepted. After a failure with EffectPossible the offset is uncertain:
// Inspect refreshes it, and the caller decides what to resend.
func (op *Operation) WriteStdin(ctx context.Context, data []byte) (int, error) {
	caps, err := op.c.capabilities(ctx)
	if err != nil {
		return 0, err
	}
	release, err := op.lockStdin(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	written := 0
	for written < len(data) {
		chunk := data[written:min(len(data), written+int(caps.MaxDataBytes))]
		m, err := op.c.do(ctx, WriteStdinRequest{OperationRef: op.ref, Offset: op.stdinOffset.Load(), Data: chunk}, nil)
		if err != nil {
			return written, err
		}
		n := int(m.(WriteStdinResponse).Accepted)
		if n > len(chunk) {
			return written, Fail(CodeUnknown, sandboxwire.EffectPossible, "service accepted %d of %d bytes", n, len(chunk))
		}
		written += n
		op.stdinOffset.Add(uint64(n))
	}
	return written, nil
}

// CloseStdin closes pipe stdin after the bytes accepted so far.
func (op *Operation) CloseStdin(ctx context.Context) error {
	release, err := op.lockStdin(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = op.c.do(ctx, CloseStdinRequest{OperationRef: op.ref, Offset: op.stdinOffset.Load()}, nil)
	return err
}

// CloseOutput closes the read side of stream.
func (op *Operation) CloseOutput(ctx context.Context, stream Stream) error {
	_, err := op.c.do(ctx, CloseOutputRequest{OperationRef: op.ref, Stream: stream}, nil)
	return err
}

// Resize sets the PTY size.
func (op *Operation) Resize(ctx context.Context, size WindowSize) error {
	_, err := op.c.do(ctx, ResizePTYRequest{OperationRef: op.ref, Size: size}, nil)
	return err
}

// Signal delivers sig to target.
func (op *Operation) Signal(ctx context.Context, sig Signal, target SignalTarget) error {
	_, err := op.c.do(ctx, SignalRequest{OperationRef: op.ref, Signal: sig, Target: target}, nil)
	return err
}

// Cancel sends TERM to the scope, then KILL after grace milliseconds.
func (op *Operation) Cancel(ctx context.Context, graceMillis uint32) error {
	_, err := op.c.do(ctx, CancelRequest{OperationRef: op.ref, GraceMillis: graceMillis}, nil)
	return err
}

// Ack acknowledges events through sequence, letting the service reclaim them.
func (op *Operation) Ack(ctx context.Context, sequence uint64) error {
	_, err := op.c.do(ctx, AckEventsRequest{OperationRef: op.ref, Sequence: sequence}, nil)
	return err
}

// Release releases the settled operation and closes the handle.
func (op *Operation) Release(ctx context.Context) error {
	if _, err := op.c.do(ctx, ReleaseRequest{op.ref}, nil); err != nil {
		return err
	}
	op.c.unregister(op)
	return nil
}

// Detach closes the handle without affecting the operation.
func (op *Operation) Detach() { op.c.unregister(op) }
