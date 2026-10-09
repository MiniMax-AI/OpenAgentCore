package sandboxfs

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func testAttachment() Attachment {
	return Attachment{ID: sandboxwire.NewID(), ServerInstanceID: testInstance, Lease: context.Background(), Exports: []sandboxlink.ExportGrant{{ID: WorldExport}}}
}

// describer answers Describe and counts the calls.
type describer struct {
	Service
	calls atomic.Int32
}

func (d *describer) Describe(context.Context, Attachment, *DescribeRequest) (*DescribeResponse, error) {
	d.calls.Add(1)
	return &DescribeResponse{ServerInstanceID: testInstance, Capabilities: testCaps}, nil
}

// A request holds its slot until its response is written, so a client that
// stops reading responses stops admission.
func TestUnreadResponsesStopAdmission(t *testing.T) {
	cc, sc := net.Pipe()
	svc := &describer{}
	served := make(chan error, 1)
	a := testAttachment()
	go func() { served <- NewServer(svc).Serve(context.Background(), sc, a, 1) }()
	var read atomic.Int32 // net.Pipe completes a write once the server has read it
	go func() {
		for id := uint64(1); id <= 2*MaxInFlight; id++ {
			if sandboxwire.WriteFrame(cc, sandboxwire.Frame{Type: uint16(OpDescribe), RequestID: id}) != nil {
				return
			}
			read.Add(1)
		}
	}()
	// MaxInFlight requests run; the next one blocks the server writing its
	// ResourceExhausted answer, so nothing further is read.
	settled := func() bool { return svc.calls.Load() == MaxInFlight && read.Load() == MaxInFlight+1 }
	for deadline := time.Now().Add(5 * time.Second); !settled() && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if !settled() {
		t.Fatalf("%d requests served and %d read while no response was read", svc.calls.Load(), read.Load())
	}
	cc.Close()
	<-served
}

// A RequestID that does not increase ends the stream without dispatching.
func TestRequestIDsMustIncrease(t *testing.T) {
	cc, sc := net.Pipe()
	defer cc.Close()
	svc := &describer{}
	served := make(chan error, 1)
	a := testAttachment()
	go func() { served <- NewServer(svc).Serve(context.Background(), sc, a, 1) }()
	describe := sandboxwire.Frame{Type: uint16(OpDescribe), RequestID: 5}
	if err := sandboxwire.WriteFrame(cc, describe); err != nil {
		t.Fatal(err)
	}
	if _, err := sandboxwire.ReadFrame(cc, sandboxwire.MaxPayload); err != nil {
		t.Fatal(err)
	}
	if err := sandboxwire.WriteFrame(cc, describe); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if !errors.Is(err, sandboxwire.ErrMalformed) || svc.calls.Load() != 1 {
			t.Fatalf("Serve returned %v after %d calls", err, svc.calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("a repeated RequestID kept the stream after %d calls", svc.calls.Load())
	}
}

// ordered holds Attach and Open until the test lets them finish, whatever
// their context says, as a handler inside a system call does. It records the
// attachment and the handles they create.
type ordered struct {
	Service
	entered, proceed chan struct{}

	mu       sync.Mutex
	attached bool
	handles  map[HandleID]bool
}

func newOrdered() *ordered {
	return &ordered{entered: make(chan struct{}), proceed: make(chan struct{}), handles: map[HandleID]bool{}}
}

func (o *ordered) hold() {
	close(o.entered)
	<-o.proceed
}

func (o *ordered) Describe(context.Context, Attachment, *DescribeRequest) (*DescribeResponse, error) {
	return &DescribeResponse{ServerInstanceID: testInstance, Capabilities: testCaps}, nil
}

func (o *ordered) Attach(context.Context, Attachment, *AttachRequest) (*AttachResponse, error) {
	o.hold()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.attached = true
	return &AttachResponse{Root: Entry{Node: testNode, Attr: testDirAttr}}, nil
}

func (o *ordered) Detach(context.Context, Attachment, *DetachRequest) (*DetachResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.attached {
		return nil, NewFailure(CodeStaleAttachment, sandboxwire.EffectNone, "not attached")
	}
	o.attached = false
	return &DetachResponse{}, nil
}

func (o *ordered) Open(_ context.Context, _ Attachment, r *OpenRequest) (*OpenResponse, error) {
	o.hold()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.handles[r.Handle] = true
	return &OpenResponse{}, nil
}

func (o *ordered) OpenTree(_ context.Context, _ Attachment, r *OpenTreeRequest) (*OpenTreeResponse, error) {
	o.hold()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.handles[r.Handle] = true
	return &OpenTreeResponse{Size: 108}, nil
}

func (o *ordered) Release(_ context.Context, _ Attachment, r *ReleaseRequest) (*ReleaseResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.handles[r.Handle] {
		return nil, NewFailure(CodeStaleHandle, sandboxwire.EffectNone, "no such handle")
	}
	delete(o.handles, r.Handle)
	return &ReleaseResponse{}, nil
}

// leftover reports whether an attachment or handle remains.
func (o *ordered) leftover() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.attached || len(o.handles) > 0
}

func serveStream(t *testing.T, srv *Server, a Attachment, seq uint64) (*Client, chan error) {
	cc, sc := net.Pipe()
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background(), sc, a, seq) }()
	c := NewClient(cc)
	t.Cleanup(func() { c.Close() })
	return c, served
}

// A request still running on a failed stream finishes before the successor
// stream of its attachment dispatches the cleanup for it, so the cleanup
// finds what the request created.
func TestSuccessorWaitsForPredecessor(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name            string
		acquire, settle func(*Client) error
	}{
		{"Attach then Detach",
			func(c *Client) error { _, err := c.Attach(ctx, &AttachRequest{Export: WorldExport}); return err },
			func(c *Client) error { _, err := c.Detach(ctx, &DetachRequest{}); return err }},
		{"Open then Release",
			func(c *Client) error {
				_, err := c.Open(ctx, &OpenRequest{Handle: 7, Node: testNode, Access: AccessRead})
				return err
			},
			func(c *Client) error { _, err := c.Release(ctx, &ReleaseRequest{Handle: 7}); return err }},
		{"OpenTree then Release",
			func(c *Client) error {
				_, err := c.OpenTree(ctx, &OpenTreeRequest{Handle: 7, Node: testNode, MaxEntries: 1000, MaxDataBytes: 20 << 20})
				return err
			},
			func(c *Client) error { _, err := c.Release(ctx, &ReleaseRequest{Handle: 7}); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newOrdered()
			srv := NewServer(svc)
			waiting := make(chan struct{})
			srv.awaitPredecessor = func() { close(waiting) }
			a := testAttachment()

			first, served := serveStream(t, srv, a, 1)
			acquired := make(chan error, 1)
			go func() { acquired <- tc.acquire(first) }()
			<-svc.entered

			// The client gives up on the first stream and resumes on a second.
			second, _ := serveStream(t, srv, a, 2)
			settled := make(chan error, 1)
			go func() { settled <- tc.settle(second) }()
			select {
			case <-waiting:
			case err := <-settled:
				t.Fatalf("the successor served the cleanup while the predecessor ran: %v", err)
			}
			close(svc.proceed)

			if err := <-settled; err != nil {
				t.Fatalf("cleanup on the successor: %v", err)
			}
			if err := <-acquired; !errors.Is(err, ErrTransport) {
				t.Fatalf("the superseded stream answered: %v", err)
			}
			if err := <-served; !errors.Is(err, ErrSuperseded) {
				t.Fatalf("superseded Serve returned %v", err)
			}
			if svc.leftover() {
				t.Fatal("the cleanup left state behind")
			}
		})
	}
}

// A successor waiting behind a predecessor whose handler is stuck ends as soon
// as a newer stream supersedes it. The newest stream keeps waiting for the
// stuck handler: its Detach finds the attachment that handler creates.
func TestSupersededWaiterEnds(t *testing.T) {
	ctx := context.Background()
	svc := newOrdered()
	srv := NewServer(svc)
	waiting := make(chan struct{}, 2)
	srv.awaitPredecessor = func() { waiting <- struct{}{} }
	a := testAttachment()
	first, _ := serveStream(t, srv, a, 1)
	go first.Attach(ctx, &AttachRequest{Export: WorldExport})
	<-svc.entered

	second, secondServed := serveStream(t, srv, a, 2)
	go second.Detach(ctx, &DetachRequest{})
	<-waiting
	third, _ := serveStream(t, srv, a, 3)
	select {
	case err := <-secondServed:
		if !errors.Is(err, ErrSuperseded) {
			t.Fatalf("superseded Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the superseded stream still waits for the stuck handler")
	}

	settled := make(chan error, 1)
	go func() {
		_, err := third.Detach(ctx, &DetachRequest{})
		settled <- err
	}()
	<-waiting
	close(svc.proceed)
	if err := <-settled; err != nil {
		t.Fatalf("Detach on the newest stream: %v, want it to follow the Attach", err)
	}
	if svc.leftover() {
		t.Fatal("the attachment remains")
	}
}

// The end of a waiting successor's stream, and then of the attachment's lease,
// leave the fence in place: a stream of the same attachment ID under a new
// lease, as Link serves an ID bound again after its attachment closed, still
// waits for the stuck handler of the streams before it. The leases and each
// stream's context end separately.
func TestFenceOutlivesLease(t *testing.T) {
	ctx := context.Background()
	svc := newOrdered()
	srv := NewServer(svc)
	waiting := make(chan struct{}, 2)
	srv.awaitPredecessor = func() { waiting <- struct{}{} }
	lease, endLease := context.WithCancel(ctx)
	a := testAttachment()
	a.Lease = lease
	first, _ := serveStream(t, srv, a, 1)
	go first.Attach(ctx, &AttachRequest{Export: WorldExport})
	<-svc.entered

	second, secondServed := serveStream(t, srv, a, 2)
	go second.Detach(ctx, &DetachRequest{})
	<-waiting
	second.Close()
	<-secondServed
	endLease()

	renewed := a
	renewed.Lease = ctx
	third, _ := serveStream(t, srv, renewed, 3)
	settled := make(chan error, 1)
	go func() {
		_, err := third.Detach(ctx, &DetachRequest{})
		settled <- err
	}()
	select {
	case <-waiting:
	case err := <-settled:
		t.Fatalf("the later stream ran Detach beside the stuck Attach: %v", err)
	}
	close(svc.proceed)
	if err := <-settled; err != nil {
		t.Fatalf("Detach: %v, want it to follow the Attach", err)
	}
}

// A stream Link bound before the attachment's newest stream, but whose handler
// reaches Serve later, is refused without dispatching anything, also after the
// newer stream ended. The newer stream keeps serving.
func TestOlderBindIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := &describer{}
	srv := NewServer(svc)
	a := testAttachment()
	newer, served := serveStream(t, srv, a, 2)
	if _, err := newer.Describe(ctx, &DescribeRequest{}); err != nil {
		t.Fatal(err)
	}
	// The older stream's client is gone, so a Serve that admitted it would
	// end at once with EOF instead of ErrSuperseded.
	late := func() error {
		cc, sc := net.Pipe()
		cc.Close()
		return srv.Serve(ctx, sc, a, 1)
	}
	if err := late(); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("older stream: %v, want ErrSuperseded", err)
	}
	if _, err := newer.Describe(ctx, &DescribeRequest{}); err != nil {
		t.Fatalf("newer stream after the refusal: %v", err)
	}
	newer.Close()
	<-served
	if err := late(); !errors.Is(err, ErrSuperseded) || svc.calls.Load() != 2 {
		t.Fatalf("older stream after the newer ended: %v, %d calls served", err, svc.calls.Load())
	}
}

// A stream that reaches the server after its attachment's lease ended, and
// after the newer stream it was bound before has drained and been forgotten,
// is refused.
func TestEndedLeaseIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := &describer{}
	srv := NewServer(svc)
	lease, endLease := context.WithCancel(ctx)
	a := testAttachment()
	a.Lease = lease
	newer, served := serveStream(t, srv, a, 2)
	if _, err := newer.Describe(ctx, &DescribeRequest{}); err != nil {
		t.Fatal(err)
	}
	newer.Close()
	<-served
	endLease()
	cc, sc := net.Pipe()
	cc.Close()
	if err := srv.Serve(ctx, sc, a, 1); !errors.Is(err, ErrLeaseEnded) {
		t.Fatalf("late stream: %v, want ErrLeaseEnded", err)
	}
}

// On a healthy stream, a Release of a handle whose Open still runs, as after
// the client cancelled the Open, waits and releases what the Open created. A
// second acquisition of the pending ID is refused without effect.
func TestReleaseFollowsPendingAcquisition(t *testing.T) {
	for _, open := range []Request{&OpenRequest{Handle: 7, Node: testNode, Access: AccessRead}, &OpenTreeRequest{Handle: 7, Node: testNode, MaxEntries: 1000, MaxDataBytes: 20 << 20}} {
		t.Run(open.Op().String(), func(t *testing.T) { testReleaseFollowsPendingAcquisition(t, open) })
	}
}
func testReleaseFollowsPendingAcquisition(t *testing.T, open Request) {
	svc := newOrdered()
	cc, sc := net.Pipe()
	defer cc.Close()
	go NewServer(svc).Serve(context.Background(), sc, testAttachment(), 1)
	responses := make(chan sandboxwire.Frame, 8)
	go func() {
		for {
			f, err := sandboxwire.ReadFrame(cc, sandboxwire.MaxPayload)
			if err != nil {
				return
			}
			responses <- f
		}
	}()
	send := func(id uint64, r Request) {
		payload, err := encodeRequest(r)
		if err == nil {
			err = sandboxwire.WriteFrame(cc, sandboxwire.Frame{Type: uint16(r.Op()), RequestID: id, Payload: payload})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	outcomes := map[uint64]*Failure{}
	receive := func() uint64 {
		f := <-responses
		_, fail, err := decodeResponse(Op(f.Type^sandboxwire.ResponseType(0)), f.Payload)
		if err != nil {
			t.Fatal(err)
		}
		outcomes[f.RequestID] = fail
		return f.RequestID
	}

	send(1, open)
	<-svc.entered
	send(2, &CancelRequestRequest{Target: 1})
	send(3, &ReleaseRequest{Handle: 7})
	send(4, open)
	send(5, &DescribeRequest{})
	// The server dispatches in order, so the Describe answer shows that it
	// has dispatched the Release.
	for receive() != 5 {
	}
	if _, ok := outcomes[3]; ok {
		t.Fatal("Release answered while its Open ran")
	}
	if f := outcomes[4]; f == nil || f.Code != CodeInvalidArgument || f.Effect != sandboxwire.EffectNone {
		t.Fatalf("second Open of a pending ID: %v", f)
	}
	close(svc.proceed)
	for len(outcomes) < 5 {
		receive()
	}
	if f := outcomes[3]; f != nil || svc.leftover() {
		t.Fatalf("Release after the Open: %v; handles left: %v", f, svc.leftover())
	}
}
