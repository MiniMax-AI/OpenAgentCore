package execution

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Each Harness declares its own image rule: Codex passes any reference to the
// model provider, Claude Code admits what its native blocks express, and a
// Harness without image input rejects every image.
func TestMessageImageRulesBelongToTheHarness(t *testing.T) {
	for _, tc := range []struct {
		kind, url string
		want      bool
	}{
		{"codex", "https://example.com/a.webp", true},
		{"codex", "data:image/bmp;base64,AQID", true},
		{"claude_sdk", "https://example.com/a.webp", true},
		{"claude_sdk", "data:image/gif;base64,AQID", true},
		{"claude_sdk", "data:image/bmp;base64,AQID", false},
		{"claude_sdk", "ftp://example.com/a.png", false},
		{"mcode", "https://example.com/a.webp", false},
	} {
		profile, _ := builtin.Registry().Lookup(tc.kind)
		payload, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": tc.url}}}}})
		err := validateDeclaredInputs(profile, []sessions.Input{{Kind: "message", Payload: payload}})
		if (err == nil) != tc.want || err != nil && !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatalf("%s %s: %v", tc.kind, tc.url, err)
		}
	}
}

func TestImageQualificationIsIndependentOfEnvironmentSource(t *testing.T) {
	image := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="
	message := sessions.Input{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","image_url":"` + image + `"}]}]}`)}
	result := sessions.Input{Kind: "tool_result", Payload: json.RawMessage(`{"call_id":"call","result":{"success":true,"output":[{"type":"input_image","image_url":"` + image + `"}]}}`)}
	for _, kind := range []string{"codex", "claude_sdk", "mcode"} {
		profile, _ := builtin.Registry().Lookup(kind)
		for _, placement := range []string{"none", "openai_hosted", "self_hosted"} {
			err := validateDeclaredInputs(profile, []sessions.Input{message})
			want := kind != "mcode"
			if (err == nil) != want {
				t.Fatalf("%s/%s: %v", kind, placement, err)
			}
		}
	}
	profile, _ := builtin.Registry().Lookup("claude_sdk")
	if err := validateDeclaredInputs(profile, []sessions.Input{result}); err != nil {
		t.Fatal(err)
	}
	result.Payload = json.RawMessage(`{"call_id":"call","result":{"success":false,"output":[{"type":"input_image","image_url":"` + image + `"}]}}`)
	if err := validateDeclaredInputs(profile, []sessions.Input{result}); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("native failed image admitted", err)
	}
}
