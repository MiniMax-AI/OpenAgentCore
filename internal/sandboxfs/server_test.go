package sandboxfs

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

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
	a := Attachment{ID: sandboxwire.NewID(), ServerInstanceID: testInstance, Lease: context.Background(), Exports: []sandboxlink.ExportGrant{{ID: "world"}}}
	go func() { served <- Serve(context.Background(), sc, svc, a) }()
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
	a := Attachment{ID: sandboxwire.NewID(), ServerInstanceID: testInstance, Lease: context.Background(), Exports: []sandboxlink.ExportGrant{{ID: "world"}}}
	go func() { served <- Serve(context.Background(), sc, svc, a) }()
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
