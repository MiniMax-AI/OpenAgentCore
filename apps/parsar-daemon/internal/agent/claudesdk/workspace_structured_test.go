//go:build unix

package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestWorkspaceStructuredPreparationQualificationAndFrozenSchema(t *testing.T) {
	for _, mode := range []string{"structured-missing", "structured-ready"} {
		t.Run(mode, func(t *testing.T) {
			config := preparationFixture(t, mode)
			req := preparationRequest()
			req.ObserveMessages = true
			schema := `{"type":"object","properties":{"n":{"const":9007199254740992}}}`
			req.ExecutionControls = &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium", OutputFormat: &proto.OutputFormat{Type: "json_schema", Schema: json.RawMessage(schema)}}
			p, err := NewPreparationFactory(config)(t.Context(), req)
			if mode == "structured-missing" {
				if err == nil || !strings.Contains(err.Error(), "workspace structured output") {
					t.Fatal("unqualified bundle admitted", err)
				}
				if _, err := os.Stat(filepath.Join(config.StateDir, "launched")); !os.IsNotExist(err) {
					t.Fatal("unqualified request reached native launch", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			req.ExecutionControls.OutputFormat.Schema[0] = ' '
			var frozen startRequest
			if err := json.Unmarshal(waitPreparationFile(t, filepath.Join(config.StateDir, "prepare.json")), &frozen); err != nil {
				t.Fatal(err)
			}
			if frozen.OutputFormat == nil || string(frozen.OutputFormat.Schema) != schema {
				t.Fatal("prepared native schema changed with caller memory")
			}
			out := make(chan proto.Envelope, 16)
			if _, err := p.Start(t.Context(), "run", proto.TextInput("hello"), out); err != nil {
				t.Fatal(err)
			}
			for event := range out {
				if event.Type == proto.TypeError {
					t.Fatal("prepared execution failed", string(event.Payload))
				}
			}
			var started map[string]json.RawMessage
			if err := json.Unmarshal(waitPreparationFile(t, filepath.Join(config.StateDir, "start.json")), &started); err != nil || len(started) != 2 || started["output_format"] != nil {
				t.Fatal("Start replaced the prepared configuration", err)
			}
		})
	}
}

func TestWorkspaceStructuredReadinessRequiresCompleteLocalContract(t *testing.T) {
	features := []string{"workspace_tools", "workspace_prepare", "workspace_command_observations", "local_runtime_v1", "structured_output", "workspace_structured_output"}
	if !(RuntimeInfo{Features: features}).SupportsWorkspaceStructuredOutput() {
		t.Fatal("qualified Runtime unavailable")
	}
	for i, missing := range features {
		t.Run(missing, func(t *testing.T) {
			partial := append(append([]string{}, features[:i]...), features[i+1:]...)
			if (RuntimeInfo{Features: partial}).SupportsWorkspaceStructuredOutput() {
				t.Fatal("incomplete workspace bundle admitted")
			}
		})
	}
}
