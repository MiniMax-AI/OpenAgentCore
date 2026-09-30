package execution

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestImageQualificationIsIndependentOfEnvironmentSource(t *testing.T) {
	image := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="
	message := sessions.Input{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","image_url":"` + image + `"}]}]}`)}
	result := sessions.Input{Kind: "tool_result", Payload: json.RawMessage(`{"call_id":"call","result":{"success":true,"output":[{"type":"input_image","image_url":"` + image + `"}]}}`)}
	for _, kind := range []string{"codex", "claude_sdk", "mcode"} {
		profile, _ := (engine.Catalog{}).Lookup(kind)
		for _, placement := range []string{"none", "openai_hosted", "self_hosted"} {
			err := validateProfileInputs(profile, placement, []sessions.Input{message})
			want := kind != "mcode"
			if (err == nil) != want {
				t.Fatalf("%s/%s: %v", kind, placement, err)
			}
		}
	}
	profile, _ := (engine.Catalog{}).Lookup("claude_sdk")
	if err := validateProfileInputs(profile, "openai_hosted", []sessions.Input{result}); err != nil {
		t.Fatal(err)
	}
	result.Payload = json.RawMessage(`{"call_id":"call","result":{"success":false,"output":[{"type":"input_image","image_url":"` + image + `"}]}}`)
	if err := validateProfileInputs(profile, "openai_hosted", []sessions.Input{result}); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("native failed image admitted", err)
	}
}
