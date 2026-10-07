package integration

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestManagedRuntimeResetPreservesCancelAndRetry(t *testing.T) {
	s, _ := newManagedTestStore(t)
	key := webDeployment(t, s, "e2b")
	tenant, session, _ := managedSession(t, s)
	inputs := []sessions.Input{messageInput("accepted work")}
	accepted, err := submitInputs(t.Context(), s, tenant, session.ID, "work", inputs)
	if err != nil {
		t.Fatal(err)
	}
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	w, stop := managedWorker(t, s, key, p)
	defer stop()
	if _, err := w.StartSandboxReset(SandboxResetTestContext(t.Context()), deployment.ResetRequest{Clear: "auto", ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateSession(t.Context(), tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: session.Configuration}); !errors.Is(err, placement.ErrResetAdmission) {
		t.Fatal("reset accepted new hosted Session", err)
	}
	cancel := []sessions.Input{{Kind: "cancel", Payload: json.RawMessage(`{}`)}}
	first, err := w.SubmitInputs(t.Context(), tenant, session.ID, "cancel", cancel)
	if err != nil || len(first) != 1 {
		t.Fatal("existing work cannot be cancelled", first, err)
	}
	replay, err := w.SubmitInputs(t.Context(), tenant, session.ID, "cancel", cancel)
	if err != nil || len(replay) != 1 || !replay[0].Replayed || replay[0].Sequence != first[0].Sequence || replay[0].TurnID != first[0].TurnID {
		t.Fatal("cancel retry lost its receipt", replay, err)
	}
	retry, err := w.SubmitInputs(t.Context(), tenant, session.ID, "work", inputs)
	if err != nil || len(retry) != 1 || !retry[0].Replayed || retry[0].Sequence != accepted[0].Sequence || retry[0].TurnID != accepted[0].TurnID {
		t.Fatal("matching input retry lost its accepted outcome", retry, err)
	}
	if _, err := w.SubmitInputs(t.Context(), uuid.NewString(), session.ID, "cancel", cancel); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("reset weakened tenant isolation", err)
	}
	if p.creates != 0 {
		t.Fatal("existing controls provisioned a new Runtime")
	}
}
