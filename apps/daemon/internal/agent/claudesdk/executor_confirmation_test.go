//go:build unix

package claudesdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestExecutorNativeConfirmationSurvivesCleanup(t *testing.T) {
	for _, mode := range []string{"unknown_cancel", "queued_cancel", "missing_confirmation", "pending_function", "pending_function_unconfirmed", "pending_input", "confirmed_closed"} {
		t.Run(mode, func(t *testing.T) {
			config, req := persistentConfig(t, mode)
			if mode == "pending_function" || mode == "pending_function_unconfirmed" {
				req.FunctionTools = []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
			}
			owner, err := NewExecutorFactory(config)(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close(context.Background())
			turn, out := consumeExecutorTurn(t, owner, "cancel", "wait")
			if event := <-out; event.Type != proto.TypeDelta {
				t.Fatal("initial output missing")
			}
			if mode == "pending_function" || mode == "pending_function_unconfirmed" {
				if event := <-out; event.Type != proto.TypeFunctionCall {
					t.Fatal("function obligation missing")
				}
			}
			var inputReceipt chan error
			if mode == "pending_input" {
				inputReceipt = make(chan error, 1)
				go func() {
					inputReceipt <- turn.(*session).SteerWithReceipt(t.Context(), proto.PromptSteerPayload{InputID: "input", Input: proto.TextInput("extra")}, nil)
				}()
				if event := <-out; event.Type != proto.TypeDelta {
					t.Fatal("input write barrier missing")
				}
			}
			cancelErr := turn.Cancel(t.Context())
			for range out {
			}
			settlement, settlementErr := turn.AwaitSettlement(t.Context())
			confirmed := mode == "confirmed_closed" || mode == "pending_function"
			reusable := mode == "pending_function"
			if (cancelErr == nil) != confirmed || (settlementErr == nil) != confirmed || settlement.Reusable != reusable {
				t.Fatalf("cancel=%v settlement=%+v err=%v", cancelErr, settlement, settlementErr)
			}
			if inputReceipt != nil {
				if err := <-inputReceipt; err == nil {
					t.Fatal("missing native input receipt was confirmed")
				}
			}
			// Resource cleanup succeeds independently. It must not erase the immutable
			// native outcome used by repeat cancellation and Runtime acknowledgement.
			if err := owner.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if (turn.Cancel(t.Context()) == nil) != confirmed {
				t.Fatal("Close changed cancellation confirmation")
			}
			if _, err := turn.AwaitSettlement(t.Context()); (err == nil) != confirmed {
				t.Fatal("Close changed Turn confirmation")
			}
		})
	}
}
