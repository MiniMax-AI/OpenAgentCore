package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func imageAdmissionBatch() []sessions.Input {
	return []sessions.Input{
		messageInput("do not partially admit"),
		{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="}]}]}`)},
	}
}

// MiniMax Code declares no message images, so an image rejects the whole
// batch before anything persists.
func TestUnsupportedImageAdmissionIsAtomic(t *testing.T) {
	for _, placement := range []string{"none", "self_hosted"} {
		t.Run(placement, func(t *testing.T) {
			h := newDispatchHarness(t)
			worker := startWorker(t, t.Context(), h.s, h.d)
			defer func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); _ = worker.Run(ctx) }()
			configuration := json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"` + placement + `","workspace_directory":"/workspace"}}`)
			create := sessions.CreateSession{Creator: FixtureCreator(), Engine: "mcode", IdempotencyKey: "image-create", Configuration: configuration, InitialInputs: imageAdmissionBatch()}
			var unsupported *proto.SelectionError
			if _, err := worker.CreateSession(t.Context(), h.tenant, create); !errors.As(err, &unsupported) {
				t.Fatal("creation accepted an unsupported image", err)
			}
			// Create a Session without starting work to exercise both ordinary and
			// prepared admission before their respective persistence paths.
			create.InitialInputs = nil
			session, err := h.s.CreateSession(t.Context(), h.tenant, create)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := worker.SubmitInputs(t.Context(), h.tenant, session.ID, "image-batch", imageAdmissionBatch()); !errors.As(err, &unsupported) {
				t.Fatal("batch accepted an unsupported image", err)
			}
			session, err = sessionAdapter(h.s).GetSession(t.Context(), h.tenant, session.ID)
			if err != nil || session.LastTurn != nil || session.EnvironmentInputActivity != nil {
				t.Fatal("rejected batch persisted execution activity", err)
			}
		})
	}
}
