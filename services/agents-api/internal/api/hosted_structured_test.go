package api

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
)

func TestHostedStructuredConfigurationQualification(t *testing.T) {
	format := `"text":{"format":{"type":"json_schema","schema":{"type":"object"}}}`
	for _, fields := range []string{
		`"skills":[` + string(skillInput(t, "Use native tools.")) + `]`,
		`"plugins":[` + string(pluginInput(t)) + `]`,
		`"capability_directories":["/workspace/capabilities"]`,
	} {
		template, err := decodeTemplateInput(json.RawMessage(`{` + fields + `}`))
		if err != nil {
			t.Fatal(err)
		}
		lookup := &templateLookupStore{network: "enabled", skills: template.Initialization.Skills,
			plugins: template.Initialization.Plugins, directories: template.Initialization.CapabilityDirectories}
		h := Handler{store: lookup}
		for _, environment := range []string{
			`{"type":"openai_hosted",` + fields + `}`,
			`{"type":"openai_hosted","environment_template_id":"saved"}`,
			`{"type":"openai_hosted","environment_template_id":"saved","skills":[],"plugins":[],"capability_directories":[]}`,
		} {
			var decoded decodedSessionRequest
			if err := json.Unmarshal([]byte(`{"agent":{"model":"model",`+format+`},"environment":`+environment+`}`), &decoded); err != nil {
				t.Fatal(err)
			}
			input, err := decoded.validated()
			if err != nil {
				t.Fatal(err)
			}
			if err := h.resolveTemplateEnvironment(t.Context(), "tenant", &input); err != nil {
				t.Fatal(err)
			}
			configuration, err := resolve(input, "tenant", "key", nil)
			if err != nil {
				t.Fatal(err)
			}
			wantAccepted := len(input.Environment.Skills)+len(input.Environment.Plugins)+len(input.Environment.CapabilityDirectories) == 0
			if err := (execution.Policy{}).ValidateSessionConfiguration("claude_sdk", configuration); (err == nil) != wantAccepted {
				t.Fatal("incorrect inline/template qualification", environment, err)
			}
			if !wantAccepted {
				input.Agent.Text.Format = json.RawMessage(`{"type":"text"}`)
				configuration, err = resolve(input, "tenant", "key", nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := (execution.Policy{}).ValidateSessionConfiguration("claude_sdk", configuration); err != nil {
					t.Fatal("ordinary workspace profile changed", err)
				}
			}
		}
	}
}
