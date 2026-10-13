package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestThreadStartParams_OmitsEmptyOptionalFields pins that model_provider
// and other optional fields don't leak null/empty values into the wire.
// codex would reject malformed enum values otherwise.
func TestThreadStartParams_OmitsEmptyOptionalFields(t *testing.T) {
	params := ThreadStartParams{
		Cwd: "/workspace",
		// Model, ModelProvider, DeveloperInstructions deliberately empty
	}
	raw, _ := json.Marshal(params)
	body := string(raw)
	for _, leak := range []string{
		`"model":""`,
		`"modelProvider":""`,
		`"developerInstructions":""`,
	} {
		if strings.Contains(body, leak) {
			t.Errorf("empty optional field leaked: %s in %s", leak, body)
		}
	}
}

// TestThreadStartParams_ModelProviderIsCamelCaseField confirms the
// model_provider override (used by injectCodexManagedModel to pin codex
// to the [model_providers.oac] config block) actually reaches
// codex via the v2 thread/start params, not just via the -c CLI
// override. Sending it on both paths is belt + suspenders.
func TestThreadStartParams_ModelProviderIsCamelCaseField(t *testing.T) {
	params := ThreadStartParams{
		Cwd:           "/workspace",
		Model:         "gpt-5.5",
		ModelProvider: "oac",
	}
	raw, _ := json.Marshal(params)
	body := string(raw)
	if !strings.Contains(body, `"modelProvider":"oac"`) {
		t.Fatalf("modelProvider missing or wrong case in wire: %s", body)
	}
	// snake_case would silently be ignored by codex's serde rename_all
	// = camelCase, so guard against accidental drift.
	if strings.Contains(body, `"model_provider"`) {
		t.Fatalf("snake_case model_provider leaked: %s", body)
	}
}

func TestTurnStartParams_CollaborationModeWireShape(t *testing.T) {
	developerInstructions := "stay within the configured workspace"
	params := TurnStartParams{
		ThreadID: "thread-1",
		Input:    []UserInput{{Type: UserInputText, Text: "ask me a question"}},
		CollaborationMode: &CollaborationMode{
			Mode: CollaborationModeDefault,
			Settings: CollaborationModeSettings{
				Model:                 "MiniMax-M3",
				DeveloperInstructions: &developerInstructions,
			},
		},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"collaborationMode":{"mode":"default","settings":{"model":"MiniMax-M3","developer_instructions":"stay within the configured workspace"}}`) {
		t.Fatalf("collaboration mode missing or malformed: %s", body)
	}
	if strings.Contains(body, `"collaboration_mode"`) {
		t.Fatalf("snake_case collaboration_mode leaked: %s", body)
	}
}
