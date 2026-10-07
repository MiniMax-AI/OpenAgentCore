package claudesdk

import (
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestModelIsRequired(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  proto.PromptRequestPayload
		want string
	}{
		{"no system prompt", proto.PromptRequestPayload{Model: "test-model"}, ""},
		{"system prompt", proto.PromptRequestPayload{Model: "test-model", SystemPrompt: "instructions"}, "instructions"},
		{"no model", proto.PromptRequestPayload{SystemPrompt: "instructions"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("OAC_RUNTIME_HOME", root)
			config := Config{Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state")}
			tc.req.RunID, tc.req.Input = "run", proto.TextInput("hello")
			start, _, err := prepare(config, tc.req)
			if (err != nil) != (tc.req.Model == "") || start.SystemPrompt != tc.want {
				t.Fatalf("system prompt %q, error %v", start.SystemPrompt, err)
			}
		})
	}
}
