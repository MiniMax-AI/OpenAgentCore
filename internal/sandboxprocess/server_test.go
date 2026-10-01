package sandboxprocess

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

type countingService struct {
	Service
	describes int
}

func (s *countingService) Describe(context.Context, *Conn, DescribeRequest) (DescribeResponse, error) {
	s.describes++
	caps := Capabilities{Platform: PlatformLinux, MaxStartBytes: 1024, MaxDataBytes: 1024, MaxReplayBytesPerOperation: 1024}
	return DescribeResponse{ServerInstanceID: fixtureInstance, Capabilities: caps}, nil
}

// A request whose ID does not increase ends the stream without being
// dispatched.
func TestServeRejectsNonIncreasingRequestID(t *testing.T) {
	cc, sc := net.Pipe()
	defer cc.Close()
	svc := &countingService{}
	served := make(chan error, 1)
	go func() { served <- Serve(context.Background(), sc, Attachment{ID: fixtureOp}, svc) }()

	describe := sandboxwire.Frame{Type: OpDescribe, RequestID: 2, Payload: Encode(DescribeRequest{})}
	if err := sandboxwire.WriteFrame(cc, describe); err != nil {
		t.Fatal(err)
	}
	if _, err := sandboxwire.ReadFrame(cc, sandboxwire.MaxPayload); err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, cc)
	describe.RequestID = 1
	if err := sandboxwire.WriteFrame(cc, describe); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if !errors.Is(err, ErrProtocol) || svc.describes != 1 {
			t.Fatalf("Serve: %v after %d dispatches, want ErrProtocol after 1", err, svc.describes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve accepted a repeated request ID")
	}
}
