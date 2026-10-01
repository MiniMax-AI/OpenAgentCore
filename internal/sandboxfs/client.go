package sandboxfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// ErrTransport is the cause of a failure the client reports because the
// stream failed or closed. errors.Is finds it through the *Failure.
var ErrTransport = errors.New("sandboxfs: transport lost")

// Client issues File requests over one stream. Its methods are safe for
// concurrent use; each returns a *Failure on failure.
type Client struct {
	conn io.ReadWriteCloser
	wsem chan struct{}               // held while a RequestID is allocated and its frame written
	seq  sandboxwire.RequestSequence // used only while wsem is held

	mu        sync.Mutex
	pending   map[uint64]*call
	abandoned map[uint64]Op // requests whose response is discarded
	err       error
	done      chan struct{}

	afterWrite func() // test seam: runs once a frame is recorded as written
}

type call struct {
	op Op
	ch chan outcome
}

type outcome struct {
	msg  message
	fail *Failure
}

// NewClient starts a client on conn. The client owns conn and closes it on
// Close or when the stream fails.
func NewClient(conn io.ReadWriteCloser) *Client {
	c := &Client{conn: conn, wsem: make(chan struct{}, 1), pending: map[uint64]*call{}, abandoned: map[uint64]Op{}, done: make(chan struct{})}
	go c.readLoop()
	return c
}

// Close closes the stream. Requests in flight fail with EffectPossible.
func (c *Client) Close() error {
	c.shutdown(errClientClosed)
	return nil
}

var errClientClosed = errors.New("client closed")

// Done is closed once the stream has failed or closed.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns why the stream ended, or nil while it is open.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func transportFailure(effect sandboxwire.Effect, cause error) *Failure {
	f := NewFailure(CodeUnknown, effect, "transport lost: "+cause.Error())
	f.cause = fmt.Errorf("%w: %w", ErrTransport, cause)
	return f
}

func (c *Client) shutdown(cause error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = cause
	pending := c.pending
	c.pending, c.abandoned = nil, nil
	close(c.done)
	c.mu.Unlock()
	c.conn.Close()
	for _, cl := range pending {
		cl.ch <- outcome{fail: transportFailure(sandboxwire.EffectPossible, cause)}
	}
}

// send registers a request and writes it. A request that ends before its
// write starts fails with EffectNone. Cancelling ctx during the write fails
// the stream, since a partial frame cannot be taken back, and the request
// then fails with EffectPossible. Once the write is recorded as finished, a
// cancellation is left to roundTrip, which sends CancelRequest.
func (c *Client) send(ctx context.Context, op Op, payload []byte) (uint64, *call, *Failure) {
	if fail := c.acquire(ctx); fail != nil {
		return 0, nil, fail
	}
	defer c.release()
	if err := ctx.Err(); err != nil {
		return 0, nil, contextFailure(err, sandboxwire.EffectNone)
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return 0, nil, transportFailure(sandboxwire.EffectNone, err)
	}
	id, cl := c.seq.Next(), &call{op: op, ch: make(chan outcome, 1)}
	c.pending[id] = cl
	c.mu.Unlock()
	// The write and the cancellation callback race for state; whichever
	// moves it from writing decides. The callback is settled before the
	// write slot is released, so it never acts on a later write.
	const writing, written, interrupted = 0, 1, 2
	var state atomic.Int32
	settled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(settled)
		if state.CompareAndSwap(writing, interrupted) {
			c.shutdown(fmt.Errorf("request cancelled while being written: %w", context.Cause(ctx)))
		}
	})
	err := sandboxwire.WriteFrame(c.conn, sandboxwire.Frame{Type: uint16(op), RequestID: id, Payload: payload})
	state.CompareAndSwap(writing, written)
	if c.afterWrite != nil {
		c.afterWrite()
	}
	if !stop() {
		<-settled
	}
	if err != nil {
		c.shutdown(err)
	}
	return id, cl, nil
}

// acquire takes the write turn unless ctx ends or the stream fails first.
func (c *Client) acquire(ctx context.Context) *Failure {
	select {
	case c.wsem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return contextFailure(ctx.Err(), sandboxwire.EffectNone)
	case <-c.done:
		return transportFailure(sandboxwire.EffectNone, c.Err())
	}
}

func (c *Client) release() { <-c.wsem }

// abandon stops waiting for request id. It reports false when the outcome
// has already been delivered.
func (c *Client) abandon(id uint64, cl *call) bool {
	c.mu.Lock()
	if c.pending[id] != cl {
		c.mu.Unlock()
		return false
	}
	delete(c.pending, id)
	c.abandoned[id] = cl.op
	c.mu.Unlock()
	go c.cancel(id)
	return true
}

func (c *Client) cancel(target uint64) {
	payload, err := encodeRequest(&CancelRequestRequest{Target: target})
	if err != nil || c.acquire(context.Background()) != nil {
		return
	}
	defer c.release()
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	id := c.seq.Next()
	c.abandoned[id] = OpCancelRequest
	c.mu.Unlock()
	if err := sandboxwire.WriteFrame(c.conn, sandboxwire.Frame{Type: uint16(OpCancelRequest), RequestID: id, Payload: payload}); err != nil {
		c.shutdown(err)
	}
}

func (c *Client) readLoop() {
	for {
		f, err := sandboxwire.ReadFrame(c.conn, sandboxwire.MaxPayload)
		if err == nil {
			err = c.deliver(f)
		}
		if err != nil {
			c.shutdown(err)
			return
		}
	}
}

// deliver routes one response. Anything but a well-formed response to an
// outstanding request is a protocol violation that ends the stream.
func (c *Client) deliver(f sandboxwire.Frame) error {
	kind, err := tags.Classify(f.Type)
	if err != nil {
		return err
	}
	if kind != sandboxwire.KindResponse {
		return malformed("message type %#04x from the server", f.Type)
	}
	op := Op(f.Type ^ sandboxwire.ResponseType(0))
	msg, fail, err := decodeResponse(op, f.Payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if cl, ok := c.pending[f.RequestID]; ok && cl.op == op {
		delete(c.pending, f.RequestID)
		c.mu.Unlock()
		cl.ch <- outcome{msg: msg, fail: fail}
		return nil
	}
	if abandoned, ok := c.abandoned[f.RequestID]; ok && abandoned == op {
		delete(c.abandoned, f.RequestID)
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return malformed("unexpected %s response to request %d", op, f.RequestID)
}

func roundTrip[R message](ctx context.Context, c *Client, q Request) (R, error) {
	var zero R
	if err := ctx.Err(); err != nil {
		return zero, contextFailure(err, sandboxwire.EffectNone)
	}
	payload, err := encodeRequest(q)
	if err != nil {
		f := NewFailure(CodeInvalidArgument, sandboxwire.EffectNone, err.Error())
		f.cause = err
		return zero, f
	}
	id, cl, fail := c.send(ctx, q.Op(), payload)
	if fail != nil {
		return zero, fail
	}
	var out outcome
	select {
	case out = <-cl.ch:
	case <-ctx.Done():
		if c.abandon(id, cl) {
			effect := sandboxwire.EffectPossible
			if sideEffectFree(q) {
				effect = sandboxwire.EffectNone
			}
			return zero, contextFailure(ctx.Err(), effect)
		}
		out = <-cl.ch
	}
	if out.fail != nil {
		return zero, out.fail
	}
	return out.msg.(R), nil
}

func contextFailure(err error, effect sandboxwire.Effect) *Failure {
	code := CodeCancelled
	if errors.Is(err, context.DeadlineExceeded) {
		code = CodeDeadlineExceeded
	}
	f := NewFailure(code, effect, err.Error())
	f.cause = err
	return f
}

func (c *Client) Describe(ctx context.Context, r *DescribeRequest) (*DescribeResponse, error) {
	return roundTrip[*DescribeResponse](ctx, c, r)
}

func (c *Client) Attach(ctx context.Context, r *AttachRequest) (*AttachResponse, error) {
	return roundTrip[*AttachResponse](ctx, c, r)
}

func (c *Client) Detach(ctx context.Context, r *DetachRequest) (*DetachResponse, error) {
	return roundTrip[*DetachResponse](ctx, c, r)
}

func (c *Client) Lookup(ctx context.Context, r *LookupRequest) (*LookupResponse, error) {
	return roundTrip[*LookupResponse](ctx, c, r)
}

func (c *Client) Walk(ctx context.Context, r *WalkRequest) (*WalkResponse, error) {
	return roundTrip[*WalkResponse](ctx, c, r)
}

func (c *Client) GetAttr(ctx context.Context, r *GetAttrRequest) (*GetAttrResponse, error) {
	return roundTrip[*GetAttrResponse](ctx, c, r)
}

func (c *Client) SetAttr(ctx context.Context, r *SetAttrRequest) (*SetAttrResponse, error) {
	return roundTrip[*SetAttrResponse](ctx, c, r)
}

func (c *Client) Access(ctx context.Context, r *AccessRequest) (*AccessResponse, error) {
	return roundTrip[*AccessResponse](ctx, c, r)
}

func (c *Client) Open(ctx context.Context, r *OpenRequest) (*OpenResponse, error) {
	return roundTrip[*OpenResponse](ctx, c, r)
}

func (c *Client) Create(ctx context.Context, r *CreateRequest) (*CreateResponse, error) {
	return roundTrip[*CreateResponse](ctx, c, r)
}

func (c *Client) Read(ctx context.Context, r *ReadRequest) (*ReadResponse, error) {
	return roundTrip[*ReadResponse](ctx, c, r)
}

func (c *Client) Write(ctx context.Context, r *WriteRequest) (*WriteResponse, error) {
	return roundTrip[*WriteResponse](ctx, c, r)
}

func (c *Client) Flush(ctx context.Context, r *FlushRequest) (*FlushResponse, error) {
	return roundTrip[*FlushResponse](ctx, c, r)
}

func (c *Client) Fsync(ctx context.Context, r *FsyncRequest) (*FsyncResponse, error) {
	return roundTrip[*FsyncResponse](ctx, c, r)
}

func (c *Client) Release(ctx context.Context, r *ReleaseRequest) (*ReleaseResponse, error) {
	return roundTrip[*ReleaseResponse](ctx, c, r)
}

func (c *Client) OpenDir(ctx context.Context, r *OpenDirRequest) (*OpenDirResponse, error) {
	return roundTrip[*OpenDirResponse](ctx, c, r)
}

func (c *Client) ReadDir(ctx context.Context, r *ReadDirRequest) (*ReadDirResponse, error) {
	return roundTrip[*ReadDirResponse](ctx, c, r)
}

func (c *Client) ReleaseDir(ctx context.Context, r *ReleaseDirRequest) (*ReleaseDirResponse, error) {
	return roundTrip[*ReleaseDirResponse](ctx, c, r)
}

func (c *Client) Mkdir(ctx context.Context, r *MkdirRequest) (*MkdirResponse, error) {
	return roundTrip[*MkdirResponse](ctx, c, r)
}

func (c *Client) Unlink(ctx context.Context, r *UnlinkRequest) (*UnlinkResponse, error) {
	return roundTrip[*UnlinkResponse](ctx, c, r)
}

func (c *Client) Rmdir(ctx context.Context, r *RmdirRequest) (*RmdirResponse, error) {
	return roundTrip[*RmdirResponse](ctx, c, r)
}

func (c *Client) Rename(ctx context.Context, r *RenameRequest) (*RenameResponse, error) {
	return roundTrip[*RenameResponse](ctx, c, r)
}

func (c *Client) Link(ctx context.Context, r *LinkRequest) (*LinkResponse, error) {
	return roundTrip[*LinkResponse](ctx, c, r)
}

func (c *Client) Symlink(ctx context.Context, r *SymlinkRequest) (*SymlinkResponse, error) {
	return roundTrip[*SymlinkResponse](ctx, c, r)
}

func (c *Client) Readlink(ctx context.Context, r *ReadlinkRequest) (*ReadlinkResponse, error) {
	return roundTrip[*ReadlinkResponse](ctx, c, r)
}

func (c *Client) StatFS(ctx context.Context, r *StatFSRequest) (*StatFSResponse, error) {
	return roundTrip[*StatFSResponse](ctx, c, r)
}

func (c *Client) Forget(ctx context.Context, r *ForgetRequest) (*ForgetResponse, error) {
	return roundTrip[*ForgetResponse](ctx, c, r)
}

func (c *Client) GetLock(ctx context.Context, r *GetLockRequest) (*GetLockResponse, error) {
	return roundTrip[*GetLockResponse](ctx, c, r)
}

func (c *Client) SetLock(ctx context.Context, r *SetLockRequest) (*SetLockResponse, error) {
	return roundTrip[*SetLockResponse](ctx, c, r)
}
