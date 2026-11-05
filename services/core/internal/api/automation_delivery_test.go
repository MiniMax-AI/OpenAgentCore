package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/automations"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// A server error or a busy Turn keeps the invocation queued; only a client
// error settles it as failed.
func TestAutomationDeliveryRetriesTemporaryErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		done bool
	}{
		{errors.New("database down"), false},
		{sessions.ErrTurnConflict, false},
		{sessions.ErrNotFound, true},
	} {
		deps, fakes := testDependencies(t)
		fakes.inputAdmission.submitInputs = func(context.Context, string, string, string, []sessions.Input) ([]sessions.InputReceipt, error) {
			return nil, tc.err
		}
		h := &Handler{Dependencies: deps}
		request, _ := json.Marshal(map[string]string{"session_id": "s", "instructions": "go"})
		outcome, done := h.deliverAutomation(context.Background(), automations.Delivery{InvocationID: "i", TargetType: "existing_session", Request: request})
		if done != tc.done || (done && outcome.Status != "failed") {
			t.Fatalf("%v: done=%v outcome=%+v", tc.err, done, outcome)
		}
	}
}
