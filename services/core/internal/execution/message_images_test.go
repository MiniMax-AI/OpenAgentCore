package execution

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Each Harness declares its own image rule: Codex rejects remote URLs as
// native does, Claude Code admits what its native blocks express, and a Harness
// without image input rejects every image.
func TestMessageImageRulesBelongToTheHarness(t *testing.T) {
	for _, tc := range []struct {
		kind, url string
		want      bool
	}{
		{"codex", "Https://example.com/a.webp", false},
		{"codex", "data:image/bmp;base64,AQID", true},
		{"claude_sdk", "https://example.com/a.webp", true},
		{"claude_sdk", "data:image/gif;base64,AQID", true},
		{"claude_sdk", "data:image/bmp;base64,AQID", false},
		{"claude_sdk", "ftp://example.com/a.png", true},
		{"claude_sdk", "data:image/png;base64,?", false},
		{"mcode", "https://example.com/a.webp", false},
	} {
		profile, _ := builtin.Registry().Lookup(tc.kind)
		payload, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": tc.url}}}}})
		err := validateDeclaredInputs(profile, []sessions.Input{{Kind: "message", Payload: payload}})
		if (err == nil) != tc.want || (err != nil && !errors.Is(err, sessions.ErrInvalidInput)) {
			t.Fatalf("%s %s: %v", tc.kind, tc.url, err)
		}
	}
}
