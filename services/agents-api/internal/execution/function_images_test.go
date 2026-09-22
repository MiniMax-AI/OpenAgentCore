package execution

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestFunctionImageAdmission(t *testing.T) {
	image := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="
	profile, _ := (engine.Catalog{}).Lookup("claude_sdk")
	for _, tc := range []struct {
		name, url      string
		success, valid bool
	}{
		{"success", image, true, true},
		{"native error drops images", image, false, false},
		{"remote", "https://example.test/image.png", true, false},
		{"invalid base64", "data:image/png;base64,?", true, false},
		{"invalid image", "data:image/png;base64,AQID", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := []any{map[string]any{"type": "input_text", "text": "before"}, map[string]any{"type": "input_image", "image_url": tc.url}, map[string]any{"type": "input_text", "text": "after"}}
			raw, _ := json.Marshal(map[string]any{"call_id": "call", "result": map[string]any{"success": tc.success, "output": output}})
			err := validateProfileInputs(profile, "none", []store.Input{{Kind: "tool_result", Payload: raw}})
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, store.ErrInvalidInput) {
				t.Fatal(err)
			}
		})
	}
	for _, placement := range []string{"self_hosted"} {
		url := image
		if err := profile.ValidateFunctionResult(placement, proto.FunctionResultPayload{Success: true, Content: []proto.InputContent{{Type: "input_image", ImageURL: &url}}}); !errors.Is(err, engine.ErrInvalidInput) {
			t.Fatal("unqualified image placement", placement, err)
		}
		if err := profile.ValidateFunctionResult(placement, proto.FunctionResultPayload{Success: true, Content: proto.TextInput("text")[0].Content}); err != nil {
			t.Fatal("workspace text regressed", err)
		}
	}
	// Readiness for image-bearing results cannot become a global function gate.
	if err := requireFunctionResultImages(nil, "unknown", proto.FunctionResultPayload{Content: proto.TextInput("text")[0].Content}); err != nil {
		t.Fatal(err)
	}
	if err := requireFunctionResultImages(&gateway.Session{}, "unknown", proto.FunctionResultPayload{Content: []proto.InputContent{{Type: "input_image", ImageURL: &image}}}); err == nil {
		t.Fatal("image delivery accepted without Runtime support")
	}
}
