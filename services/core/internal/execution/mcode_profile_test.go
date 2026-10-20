package execution

import "testing"

func TestMCodeOperationQualification(t *testing.T) {
	p := Policy{}
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"text", `{"agent":{"model":"real-model"},"environment":{"type":"none"}}`, true},
		{"hosted", `{"agent":{"model":"real-model"},"environment":{"type":"openai_hosted"}}`, true},
		{"verbosity", `{"agent":{"model":"real-model","text":{"verbosity":"high"}},"environment":{"type":"none"}}`, false},
		{"subagents", `{"agent":{"model":"real-model","multi_agent":{"enabled":true}},"environment":{"type":"none"}}`, true},
		{"functions", `{"agent":{"model":"real-model","tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]},"environment":{"type":"none"}}`, true},
		{"functions hosted", `{"agent":{"model":"real-model","tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]},"environment":{"type":"openai_hosted"}}`, true},
		{"functions with subagents", `{"agent":{"model":"real-model","multi_agent":{"enabled":true},"tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]},"environment":{"type":"none"}}`, true},
		{"reserved label", `{"agent":{"model":"real-model","tools":[{"type":"function","name":"f","parameters":{"type":"object"}},{"type":"mcp","server_label":"functions","connection_origin":"service","transport":{"type":"http","server_url":"https://example.invalid/mcp"}}]},"environment":{"type":"none"}}`, false},
		{"mcp", `{"agent":{"model":"real-model","tools":[{"type":"mcp","server_label":"x","connection_origin":"service","transport":{"type":"http","server_url":"https://example.invalid/mcp"}}]},"environment":{"type":"none"}}`, true},
		{"effort", `{"agent":{"model":"real-model","reasoning":{"effort":"high"}},"environment":{"type":"none"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := p.ValidateSessionConfiguration("mcode", []byte(tc.body))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
