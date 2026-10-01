package sandboxfs

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// A cancellation that arrives after the frame is written cancels the request
// and leaves the stream open.
func TestCancelAfterWriteKeepsStream(t *testing.T) {
	cc, sc := net.Pipe()
	a := testAttachment()
	go NewServer(&describer{}).Serve(context.Background(), sc, a, 1)
	c := NewClient(cc)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c.afterWrite = cancel
	_, err := c.Describe(ctx, &DescribeRequest{})
	var f *Failure
	if err != nil && !(errors.As(err, &f) && f.Code == CodeCancelled) {
		t.Fatalf("cancelled describe: %v", err)
	}
	c.afterWrite = nil
	if _, err := c.Describe(context.Background(), &DescribeRequest{}); err != nil || c.Err() != nil {
		t.Fatalf("describe after the cancellation: %v; stream: %v", err, c.Err())
	}
}

// held holds every Write and Close until free closes, as a transport that
// cannot send does. writing closes when the first write starts.
type held struct {
	net.Conn
	once    sync.Once
	writing chan struct{}
	free    <-chan struct{}
}

func (h *held) Write([]byte) (int, error) {
	h.once.Do(func() { close(h.writing) })
	<-h.free
	return 0, net.ErrClosed
}

func (h *held) Close() error {
	<-h.free
	return h.Conn.Close()
}

// A call cancelled while the transport holds its write, and then the
// stream's close, returns at once and fails the stream.
func TestCancelDuringHeldWrite(t *testing.T) {
	cc, sc := net.Pipe()
	defer sc.Close()
	free := make(chan struct{})
	defer close(free)
	h := &held{Conn: cc, writing: make(chan struct{}), free: free}
	c := NewClient(h)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-h.writing
		cancel()
	}()
	done := make(chan error, 1)
	go func() {
		_, err := c.Describe(ctx, &DescribeRequest{})
		done <- err
	}()
	select {
	case err := <-done:
		var f *Failure
		if !errors.As(err, &f) || f.Effect != sandboxwire.EffectPossible || !errors.Is(err, ErrTransport) {
			t.Fatalf("call cancelled mid-write: %v, want a transport failure with EffectPossible", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled call waits for the transport")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("the stream survived a cancellation mid-write")
	}
}

// finisher completes Describe after its request is cancelled, as a lock
// acquired just before CancelRequest arrives does.
type finisher struct{ Service }

func (finisher) Describe(ctx context.Context, _ Attachment, _ *DescribeRequest) (*DescribeResponse, error) {
	<-ctx.Done()
	return &DescribeResponse{ServerInstanceID: testInstance, Capabilities: testCaps}, nil
}

// An interrupt cancels the request and still returns its own outcome.
func TestInterruptReturnsOutcome(t *testing.T) {
	cc, sc := net.Pipe()
	a := testAttachment()
	go NewServer(finisher{}).Serve(context.Background(), sc, a, 1)
	c := NewClient(cc)
	defer c.Close()

	interrupt := make(chan struct{})
	close(interrupt)
	if _, err := c.Describe(WithInterrupt(context.Background(), interrupt), &DescribeRequest{}); err != nil {
		t.Fatalf("interrupted describe that completed: %v", err)
	}
}
