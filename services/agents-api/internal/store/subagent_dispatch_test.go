package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestSubagentIdentityUsesLeasedDispatchJournal(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrequested", true: "requested"}[enabled], func(t *testing.T) {
			h := newDispatchHarness(t)
			ctx := t.Context()
			configuration, _ := json.Marshal(map[string]any{
				"agent":  map[string]any{"id": "agent_root", "model": "test-model", "multi_agent": map[string]any{"enabled": enabled, "max_concurrent_subagents": 3}},
				"daemon": map[string]string{"work_dir": "/tmp"},
			})
			var err error
			h.session, err = h.s.CreateSession(ctx, h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "identity-dispatch", Configuration: configuration})
			if err != nil {
				t.Fatal(err)
			}
			if err = h.s.BindSessionDevice(ctx, h.tenant, h.session.ID, h.device.ID); err != nil {
				t.Fatal(err)
			}
			lease, err := h.s.AcquireExecutionLease(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lease.Close(context.Background()) })
			h.d.Store = lease.Store()
			input := h.message("first", "root message")
			running := h.run(ctx, input.TurnID)
			var request proto.PromptRequestPayload
			if err = h.read(proto.TypePromptRequest).DecodePayload(&request); err != nil || request.ObserveSubagentIdentities != enabled || request.MaxConcurrentSubagents == nil || *request.MaxConcurrentSubagents != 3 {
				t.Fatal("private observation policy not carried", err)
			}
			identity := proto.SubagentIdentityPayload{NativeID: "child", ParentNativeID: "root", NativeCreatedAt: 100, ParentTurnID: "native-turn", SourceItemID: "spawn-item"}
			h.write(input.TurnID, proto.TypeSubagentIdentity, identity)
			if !enabled {
				h.finished(running, store.TurnFailed)
				if _, err = h.s.GetSubagentIdentity(ctx, h.tenant, h.session.ID, "child"); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("unsolicited identity committed", err)
				}
				return
			}
			h.write(input.TurnID, proto.TypeSubagentIdentity, identity)
			childTurn := proto.SubagentTurnPayload{NativeID: "child", TurnID: "child-turn", Status: store.TurnInProgress, CreatedAtMS: 100000}
			h.write(input.TurnID, proto.TypeSubagentTurn, childTurn)
			h.write(input.TurnID, proto.TypeSubagentItem, proto.SubagentItemPayload{NativeID: "child", TurnID: "child-turn", ItemID: "answer", Position: 0, Kind: proto.TypeOutputMessage, Payload: json.RawMessage(`{"id":"answer","status":"completed","text":"child answer"}`)})
			completed := int64(101000)
			childTurn.Status, childTurn.CompletedAtMS = store.TurnCompleted, &completed
			h.write(input.TurnID, proto.TypeSubagentTurn, childTurn)
			h.write(input.TurnID, proto.TypeSubagentLifecycle, proto.SubagentLifecyclePayload{NativeID: "child", EffectID: "native-close", Status: "closed", OccurredAtMS: 102000})
			h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Content: "root result", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "root"}})
			h.finished(running, store.TurnCompleted)
			saved, err := h.s.GetSubagentIdentity(ctx, h.tenant, h.session.ID, "child")
			if err != nil || saved.NativeID != "child" || saved.ParentNativeID != "root" || saved.FirstTurnID != input.TurnID {
				t.Fatal(saved, err)
			}
			events, err := h.s.ListTurnEvents(ctx, h.tenant, h.session.ID, input.TurnID, 0, 100)
			if err != nil || len(events) != 8 || events[0].Kind != proto.TypeSubagentIdentity || events[1].Kind != proto.TypeSubagentIdentity || events[2].Kind != proto.TypeSubagentTurn || events[3].Kind != proto.TypeSubagentItem || events[5].Kind != proto.TypeSubagentLifecycle || events[6].Kind != proto.TypeDone {
				t.Fatal("journal lost identity provenance or terminal ordering", events, err)
			}
		})
	}
}
