package prototest

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestBindingsBindRuntimeValuesOnce(t *testing.T) {
	ready := status(2, "ready", "", "").Frame
	sent := func(handle string, revision uint64) proto.Envelope {
		return send(Runtime, proto.TypePreparationStatus, PreparationID, proto.PreparationStatusPayload{ExecutorID: "e-1", Handle: handle, Revision: revision, State: "ready", ExpiresAt: 7}).Frame
	}
	b := Bindings{}
	if err := b.Match(ready, sent("h-1", 3)); err == nil {
		t.Fatal("a payload difference matched")
	}
	if len(b) != 0 {
		t.Fatal("a failed match bound values")
	}
	if err := b.Match(ready, sent("h-1", 2)); err != nil {
		t.Fatal(err)
	}
	if err := b.Match(ready, sent("h-2", 2)); err == nil {
		t.Fatal("a bound placeholder accepted another value")
	}
	start, err := b.Resolve(send(Core, proto.TypeExecutionStart, PreparationID, proto.ExecutionStartPayload{ExecutorID: ExecutorID, Handle: Handle, RunID: RunID}).Frame)
	var payload proto.ExecutionStartPayload
	if err != nil || start.DecodePayload(&payload) != nil || payload.ExecutorID != "e-1" || payload.Handle != "h-1" || payload.RunID != RunID {
		t.Fatalf("resolved start: %s %v", start.Payload, err)
	}
	if SameFrame(ready, sent("h-1", 2)) == nil {
		t.Fatal("SameFrame bound a placeholder")
	}
}
