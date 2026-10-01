package sandboxprocess

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// A cancellation that arrives after the frame is written cancels the request
// and leaves the stream open.
func TestCancelAfterWriteKeepsStream(t *testing.T) {
	cc, sc := net.Pipe()
	defer sc.Close()
	go func() {
		caps := Capabilities{Platform: PlatformLinux, MaxStartBytes: 1024, MaxDataBytes: 1024, MaxReplayBytesPerOperation: 1024}
		for {
			f, err := sandboxwire.ReadFrame(sc, sandboxwire.MaxPayload)
			if err != nil {
				return
			}
			resp := DescribeResponse{ServerInstanceID: fixtureInstance, Capabilities: caps}
			if sandboxwire.WriteFrame(sc, sandboxwire.Frame{Type: resp.MessageType(), RequestID: f.RequestID, Payload: Encode(resp)}) != nil {
				return
			}
		}
	}()
	c := NewClient(cc)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c.afterWrite = cancel
	_, err := c.Describe(ctx)
	var f *Failure
	if err != nil && !(errors.As(err, &f) && f.Code == CodeCancelled && f.Effect == sandboxwire.EffectPossible) {
		t.Fatalf("cancelled describe: %v", err)
	}
	c.afterWrite = nil
	if _, err := c.Describe(context.Background()); err != nil || c.Err() != nil {
		t.Fatalf("describe after the cancellation: %v; stream: %v", err, c.Err())
	}
}
