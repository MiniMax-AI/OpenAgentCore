package execution

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/harnesstest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestMessageImageQualificationIsOperationSpecific(t *testing.T) {
	url := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="
	input := proto.MessageInput{{Content: []proto.InputContent{{Type: "input_image", ImageURL: &url}}}}
	profile := harnesstest.Declaration("fixture", func(d *harnessconfig.Declaration) { d.Capabilities.MessageImages = proto.CapabilitySupported })
	if err := validateMessageImages(profile, input); err != nil {
		t.Fatal(err)
	}
	if err := validateMessageImages(profile, input); err != nil {
		t.Fatal("qualified user machine rejected", err)
	}
	if err := validateMessageImages(harnesstest.Declaration("fixture", nil), input); !errors.Is(err, sessions.ErrInvalidInput) {
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
	batch := []sessions.Input{{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"valid first"}]}]}`)}, {Kind: "message", Payload: raw}}
	if err := validateDeclaredInputs(harnesstest.Declaration("fixture", nil), batch); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("image escaped profile validation", err)
	}
	if err := validateDeclaredInputs(profile, batch); err != nil {
		t.Fatal("qualified image rejected", err)
	}
}

// Whitespace-only text is a declared per-harness qualification, not rewritten input.
func TestWhitespaceOnlyTextQualificationUsesEngineProfiles(t *testing.T) {
	message := func(messages ...string) sessions.Input {
		raw, _ := json.Marshal(map[string]any{"input": func() []any {
			var input []any
			for _, text := range messages {
				input = append(input, map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}})
			}
			return input
		}()})
		return sessions.Input{Kind: "message", Payload: raw}
	}
	whitespace := []sessions.Input{message("   "), message("ok", "\n\t")}
	for _, kind := range []string{"codex", "claude_sdk"} {
		profile, _ := builtin.Registry().Lookup(kind)
		for _, input := range whitespace {
			if err := validateDeclaredInputs(profile, []sessions.Input{input}); err != nil {
				t.Fatal(kind, err)
			}
		}
	}
	mcode, _ := builtin.Registry().Lookup("mcode")
	for _, input := range whitespace {
		if err := validateDeclaredInputs(mcode, []sessions.Input{input}); !errors.Is(err, ErrWhitespaceOnlyText) {
			t.Fatalf("mcode %s: %v", input.Payload, err)
		}
	}
	mixed, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": " "}, map[string]any{"type": "input_text", "text": "text"}}}}})
	if err := validateDeclaredInputs(mcode, []sessions.Input{{Kind: "message", Payload: mixed}}); err != nil {
		t.Fatal("non-whitespace text rejected", err)
	}
}

// A Harness without function-result images rejects them before anything is stored.
func TestFunctionResultImagesFollowTheDeclaration(t *testing.T) {
	image := sessions.Input{Kind: "tool_result", Payload: json.RawMessage(`{"turn_id":"t","call_id":"c","result":{"success":true,"output":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="}]}}`)}
	mcode, _ := builtin.Registry().Lookup("mcode")
	if err := validateDeclaredInputs(mcode, []sessions.Input{image}); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("unsupported function-result image admitted", err)
	}
	codex, _ := builtin.Registry().Lookup("codex")
	if err := validateDeclaredInputs(codex, []sessions.Input{image}); err != nil {
		t.Fatal(err)
	}
}
