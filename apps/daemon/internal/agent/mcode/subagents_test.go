package mcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func childSnapshot(t *testing.T) nativeSubagentSnapshot {
	t.Helper()
	var snapshot nativeSubagentSnapshot
	const raw = `{"version":1,"complete":true,"rootSessionId":"root","sessions":[
 {"id":"root","turns":[{"id":"parent-turn","status":"completed","createdAt":1000,"completedAt":2000}],"tasks":[{"taskId":"task","ownerSessionId":"root","toolCallId":"spawn","status":"succeeded","metadata":{"childSessionId":"child","parentTurnId":"parent-turn","executionMode":"foreground"}}]},
 {"id":"child","parent":"root","kind":"task","name":"worker","createdAt":1100,"turns":[{"id":"turn","status":"completed","createdAt":1200,"completedAt":1800}],"messages":[{"id":"input","turnId":"turn","role":"user","data":{"msg_content":"child input"}},{"id":"answer","turnId":"turn","role":"assistant","data":{"msg_content":"child output","thinking_content":"native reasoning"}}]}]}`
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSubagentSnapshotsKeepOwnHistoryAndStableNativeIdentity(t *testing.T) {
	project := func(snapshot nativeSubagentSnapshot) []proto.Envelope {
		out := make(chan proto.Envelope, 32)
		s := &Session{ctx: context.Background(), out: out, previousNativeTurns: map[string]bool{}}
		if err := s.projectSubagents(snapshot); err != nil {
			t.Fatal(err)
		}
		close(out)
		var events []proto.Envelope
		for event := range out {
			events = append(events, event)
		}
		return events
	}
	first := project(childSnapshot(t))
	second := project(childSnapshot(t))
	if len(first) != 6 {
		t.Fatalf("got %d events", len(first))
	}
	for i, event := range first {
		if event.Type != second[i].Type || !reflect.DeepEqual(event.Payload, second[i].Payload) {
			t.Fatal("cold snapshot identities changed")
		}
	}
	if first[0].Type != proto.TypeSubagentIdentity || first[1].Type != proto.TypeSubagentTurn || first[5].Type != proto.TypeSubagentTurn {
		t.Fatal("identity/turn/item ordering changed")
	}
	var input proto.SubagentItemPayload
	if err := json.Unmarshal(first[2].Payload, &input); err != nil {
		t.Fatal(err)
	}
	if input.NativeID != "child" || input.TurnID != "turn" || input.ItemID != "input" || input.Kind != "message" {
		t.Fatalf("invalid child ownership: %+v", input)
	}
}

func TestSubagentSnapshotRejectsMissingParentProvenance(t *testing.T) {
	snapshot := childSnapshot(t)
	snapshot.Sessions[0].Tasks = nil
	s := &Session{ctx: context.Background(), out: make(chan proto.Envelope, 32)}
	if err := s.projectSubagents(snapshot); err == nil {
		t.Fatal("accepted child without original spawning provenance")
	}
}

// The ACP cancelled response may precede the root native Turn becoming terminal.
func TestSubagentSettlementIncludesActiveRootTurn(t *testing.T) {
	dir := t.TempDir()
	node, bridge := filepath.Join(dir, "node"), filepath.Join(dir, "bridge.mjs")
	for name, data := range map[string]string{
		node:   "#!/bin/sh\ncat \"$3/snapshot.json\"\n",
		bridge: "",
		filepath.Join(dir, "subagent-snapshot.mjs"): "",
	} {
		if err := os.WriteFile(name, []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OAC_RUNTIME_MCODE_NODE", node)
	t.Setenv("OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE", bridge)
	for _, status := range []string{"accepted", "aborted"} {
		t.Run(status, func(t *testing.T) {
			raw := []byte(`{"version":1,"complete":true,"rootSessionId":"root","sessions":[{"id":"root","turns":[{"id":"current","status":"` + status + `"}]}]}`)
			if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			s := &Session{ctx: ctx, sessionID: "root", opts: launchOptions{DataDir: dir}, frames: make(chan rpcFrame)}
			err := s.settleSubagents()
			if (err != nil) != (status == "accepted") {
				t.Fatalf("root %s settlement error = %v", status, err)
			}
		})
	}
}
