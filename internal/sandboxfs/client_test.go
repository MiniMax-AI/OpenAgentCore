package sandboxfs

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// A cancellation that arrives after the frame is written cancels the request
// and leaves the stream open.
func TestCancelAfterWriteKeepsStream(t *testing.T) {
	cc, sc := net.Pipe()
	a := Attachment{ID: sandboxwire.NewID(), ServerInstanceID: testInstance, Lease: context.Background(), Exports: []sandboxlink.ExportGrant{{ID: "world"}}}
	go Serve(context.Background(), sc, &describer{}, a)
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
