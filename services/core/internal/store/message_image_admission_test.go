package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func imageAdmissionBatch() []sessions.Input {
	return []sessions.Input{
		{Kind: "message", Payload: json.RawMessage(`{"text":"do not partially admit"}`)},
		{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="}]}]}`)},
	}
}

func TestUnqualifiedImageAdmissionIsAtomic(t *testing.T) {
	for _, placement := range []string{"none", "self_hosted"} {
		t.Run(placement, func(t *testing.T) {
			h := newDispatchHarness(t)
			// A registered text-only profile must stay closed regardless of the
			// adapters currently qualified by the built-in catalog.
			profile, _ := (engine.Catalog{}).Lookup("codex")
			profile.MessageImages = proto.CapabilityUnsupported
			h.d.Policy = execution.Policy{Engines: engine.NewCatalog(map[string]engine.Profile{"codex": profile})}
			worker := startWorker(t, t.Context(), h.db, h.d)
			defer func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); _ = worker.Run(ctx) }()
			configuration := json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"` + placement + `","workspace_directory":"/workspace"}}`)
			create := sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "image-create", Configuration: configuration, InitialInputs: imageAdmissionBatch()}
			if _, err := worker.CreateSession(t.Context(), h.tenant, create); !errors.Is(err, sessions.ErrInvalidInput) {
				t.Fatal("creation accepted an unqualified image", err)
			}
			// Create a Session without starting work to exercise both ordinary and
			// prepared admission before their respective persistence paths.
			create.InitialInputs = nil
			session, err := h.s.CreateSession(t.Context(), h.tenant, create)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := worker.SubmitInputs(t.Context(), h.tenant, session.ID, "image-batch", imageAdmissionBatch()); !errors.Is(err, sessions.ErrInvalidInput) {
				t.Fatal("batch accepted an unqualified image", err)
			}
			session, err = h.s.GetSession(t.Context(), h.tenant, session.ID)
			if err != nil || session.LastTurn != nil || session.EnvironmentInputActivity != nil {
				t.Fatal("rejected batch persisted execution activity", err)
			}
		})
	}
}
