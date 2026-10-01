package sandboxfs

import (
	"context"
	"errors"
	"net"
	"testing"
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
