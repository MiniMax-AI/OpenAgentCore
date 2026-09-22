package execution

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestMessageImageQualificationIsOperationSpecific(t *testing.T) {
	url := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="
	input := proto.MessageInput{{Content: []proto.InputContent{{Type: "input_image", ImageURL: &url}}}}
	profile := engine.Profile{MessageImagePlacements: []string{"none"}}
	if err := validateMessageImageProfile(profile, "none", input); err != nil {
		t.Fatal(err)
	}
	if err := validateMessageImageProfile(profile, "self_hosted", input); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("unqualified placement accepted", err)
	}
	if err := validateMessageImageProfile(engine.Profile{}, "none", input); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("unqualified profile accepted", err)
	}
	// Text admission and dispatch must not gain an online/image requirement.
	if err := (Policy{}).messageInputSupport(nil, "unknown", Snapshot{}, proto.TextInput("queued text")); err != nil {
		t.Fatal(err)
	}
	if err := requireMessageImages(nil, "unknown", proto.TextInput("active text")); err != nil {
		t.Fatal(err)
	}
	// Message validation applies even when no function-result validator exists.
	raw, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"role": "user", "content": input[0].Content}}})
	batch := []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"valid first"}`)}, {Kind: "message", Payload: raw}}
	if err := validateProfileInputs(engine.Profile{}, "none", batch); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("image escaped profile validation", err)
	}
	if err := validateProfileInputs(profile, "none", batch); err != nil {
		t.Fatal("qualified image rejected", err)
	}
}
