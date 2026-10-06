package runtimegateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

type receiptStore struct {
	mu      sync.Mutex
	receipt runtimedevice.ArchivedCancellationReceipt
}

func (r *receiptStore) ArchivedCancellationReceipt(_ context.Context, id, hash string, runs []string) (runtimedevice.ArchivedCancellationReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != "device" || hash != "original-hash" {
		return runtimedevice.ArchivedCancellationReceipt{}, nil
	}
	for _, run := range runs {
		if run == r.receipt.RunID {
			return r.receipt, nil
		}
	}
	return runtimedevice.ArchivedCancellationReceipt{}, nil
}

func TestArchivedReceiptTracksDeliveryBeyondDoneAndRejectsNewWork(t *testing.T) {
	conn := newFakeConn()
	peer := NewSession(conn, "device", "", "", NewRegistry(), nil)
	defer peer.Close("test complete")
	peer.credentialHash = "original-hash"
	receipt := runtimedevice.ArchivedCancellationReceipt{RunID: "run", Deadline: time.Now().Add(time.Second)}
	peer.heartbeat = newFakeHeartbeatStore()
	peer.archivedCancellations = &receiptStore{receipt: receipt}
	if draining, err := peer.DrainArchivedCancellation(t.Context()); err != nil || draining {
		t.Fatal("unowned delivery got drain", draining, err)
	}
	release, err := peer.TrackExecutionDelivery("run")
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := peer.SubscribeDurable("run")
	if err != nil {
		t.Fatal(err)
	}
	done, _ := proto.NewEnvelope(proto.TypeDone, "run", proto.DonePayload{})
	peer.dispatch(done)
	for range subscription.Events {
	}
	if draining, err := peer.DrainArchivedCancellation(t.Context()); err != nil || !draining {
		t.Fatal("Done discarded in-flight ACK/commit owner", draining, err)
	}
	if _, err := peer.TrackExecutionDelivery("new-run"); err == nil {
		t.Fatal("new delivery admitted while draining")
	}
	for _, kind := range []string{proto.TypePromptRequest, proto.TypePromptSteer, proto.TypeExecutionPrepare, proto.TypeRuntimePrepare, proto.TypeWorkspaceWrite, proto.TypeWorkspaceRead} {
		env, _ := proto.NewEnvelope(kind, "run", nil)
		if err := peer.Send(t.Context(), env); err == nil {
			t.Fatal("drain permitted new operation", kind)
		}
	}
	cancel, _ := proto.NewEnvelope(proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel:run"})
	if err := peer.Send(t.Context(), cancel); err != nil {
		t.Fatal("drain blocked cancellation", err)
	}
	release() // The caller has now committed its terminal result.
	if !peer.IsClosed() {
		t.Fatal("finished delivery retained revoked connection")
	}
}

type blockedControlConn struct {
	*fakeConn
	entered chan struct{}
	resume  chan struct{}
	wrote   chan struct{}
	once    sync.Once
}

func (c *blockedControlConn) WriteMessage(kind int, data []byte) error {
	c.once.Do(func() { close(c.entered) })
	<-c.resume
	defer close(c.wrote)
	return c.fakeConn.WriteMessage(kind, data)
}
func (c *blockedControlConn) WriteControl(kind int, data []byte, _ time.Time) error {
	return c.fakeConn.WriteMessage(kind, data)
}

func TestCloseWithCodeUsesConcurrentControlWriter(t *testing.T) {
	conn := &blockedControlConn{fakeConn: newFakeConn(), entered: make(chan struct{}), resume: make(chan struct{}), wrote: make(chan struct{})}
	peer := NewSession(conn, "device", "", "", NewRegistry(), nil)
	peer.Start()
	env, _ := proto.NewEnvelope(proto.TypePromptCancel, "run", nil)
	if err := peer.Send(t.Context(), env); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("data writer never entered")
	}
	finished := make(chan struct{})
	go func() { peer.CloseWithCode(CloseRuntimeDeleted, "retired"); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		close(conn.resume)
		<-finished
		t.Fatal("close used the blocked data writer")
	}
	close(conn.resume)
	select {
	case <-conn.wrote:
	case <-time.After(time.Second):
		t.Fatal("data writer did not exit")
	}
}

func TestArchivedReceiptDeadlineDoesNotRenew(t *testing.T) {
	peer := NewSession(newFakeConn(), "device", "", "", NewRegistry(), nil)
	defer peer.Close("test complete")
	peer.credentialHash = "original-hash"
	receipt := runtimedevice.ArchivedCancellationReceipt{RunID: "run", Deadline: time.Now().Add(100 * time.Millisecond)}
	peer.heartbeat = newFakeHeartbeatStore()
	peer.archivedCancellations = &receiptStore{receipt: receipt}
	release, err := peer.TrackExecutionDelivery("run")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for range 10 {
		if draining, err := peer.DrainArchivedCancellation(t.Context()); err != nil || !draining {
			t.Fatal(draining, err)
		}
	}
	select {
	case <-peer.Closed():
	case <-time.After(time.Second):
		t.Fatal("receipt retries renewed deadline")
	}
}
