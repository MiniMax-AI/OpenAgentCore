package execution

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine/enginetest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestMessageImageQualificationIsOperationSpecific(t *testing.T) {
	url := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="
	input := proto.MessageInput{{Content: []proto.InputContent{{Type: "input_image", ImageURL: &url}}}}
	profile := enginetest.Profile(func(p *engine.Profile) { p.MessageImages = proto.CapabilitySupported })
	if err := validateMessageImageProfile(profile, "none", input); err != nil {
		t.Fatal(err)
	}
	if err := validateMessageImageProfile(profile, "self_hosted", input); err != nil {
		t.Fatal("qualified user machine rejected", err)
	}
	if err := validateMessageImageProfile(enginetest.Profile(nil), "none", input); !errors.Is(err, store.ErrInvalidInput) {
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
	if err := validateProfileInputs(enginetest.Profile(nil), "none", batch); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("image escaped profile validation", err)
	}
	if err := validateProfileInputs(profile, "none", batch); err != nil {
		t.Fatal("qualified image rejected", err)
	}
}

// Whitespace-only text is a declared per-harness qualification, not rewritten input.
func TestWhitespaceOnlyTextQualificationUsesEngineProfiles(t *testing.T) {
	url := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="
	message := func(messages ...string) store.Input {
		raw, _ := json.Marshal(map[string]any{"input": func() []any {
			var input []any
			for _, text := range messages {
				input = append(input, map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}})
			}
			return input
		}()})
		return store.Input{Kind: "message", Payload: raw}
	}
	whitespace := []store.Input{message("   "), message("ok", "\n\t")}
	codex, _ := (engine.Catalog{}).Lookup("codex")
	for _, input := range whitespace {
		if err := validateProfileInputs(codex, "none", []store.Input{input}); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"claude_sdk", "mcode"} {
		profile, _ := (engine.Catalog{}).Lookup(kind)
		for _, input := range whitespace {
			for _, placement := range []string{"none", "openai_hosted", "self_hosted"} {
				if err := validateProfileInputs(profile, placement, []store.Input{input}); !errors.Is(err, ErrWhitespaceOnlyText) {
					t.Fatalf("%s %s %s: %v", kind, placement, input.Payload, err)
				}
			}
		}
	}
	claude, _ := (engine.Catalog{}).Lookup("claude_sdk")
	// Legacy text payloads use the same rule.
	if err := validateProfileInputs(claude, "none", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":" \t"}`)}}); !errors.Is(err, ErrWhitespaceOnlyText) {
		t.Fatal(err)
	}
	mixed, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": " "}, map[string]any{"type": "input_text", "text": "text"}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": " "}, map[string]any{"type": "input_image", "image_url": url}}}}})
	if err := validateProfileInputs(claude, "none", []store.Input{{Kind: "message", Payload: mixed}}); err != nil {
		t.Fatal("non-whitespace text or image rejected", err)
	}
}
