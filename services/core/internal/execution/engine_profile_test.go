package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine/enginetest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestAcceptedEnginePlacements(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk", "mcode", "unregistered"} {
		for _, placement := range []string{"none", "openai_hosted", "self_hosted"} {
			raw := json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"` + placement + `"`)
			if placement == "self_hosted" {
				raw = append(raw, []byte(`,"workspace_directory":"/workspace"`)...)
			}
			raw = append(raw, []byte(`}}`)...)
			want := engine != "unregistered"
			if err := (Policy{}).ValidateSessionConfiguration(engine, raw); (err == nil) != want {
				t.Fatalf("%s/%s: %v", engine, placement, err)
			}
		}
	}
}

func TestAdditionalProfileUsesCommonAdmission(t *testing.T) {
	configurationChecked, toolsChecked, resultChecked := false, false, false
	profile := enginetest.Profile(nil)
	profile.ConfigurationValidation = engine.AdditionalValidation
	profile.ToolsValidation = engine.AdditionalValidation
	profile.FunctionResultValidation = engine.AdditionalValidation
	profile.ValidateConfiguration = func(agent v1.Agent, environment *v1.Environment, hasDaemon bool) error {
		configurationChecked = true
		if agent.Model != "fixture" || environment == nil || hasDaemon {
			return engine.ErrInvalidInput
		}
		return nil
	}
	profile.ValidateTools = func(_ *v1.Environment, _ bool, tools []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
		toolsChecked = true
		if len(tools) != 1 || tools[0].Name != "echo" || len(mcp) != 0 {
			t.Fatal("common tool decoding did not reach profile")
		}
		return nil
	}
	profile.ValidateFunctionResult = func(placement string, result proto.FunctionResultPayload) error {
		content := result.Content
		resultChecked = true
		if placement != "none" || !result.Success || len(content) != 1 || content[0].Text == nil || *content[0].Text != "response" {
			t.Fatal("common result decoding did not reach profile")
		}
		return engine.ErrInvalidInput
	}
	catalog := engine.NewCatalog(map[string]engine.Profile{"fixture": profile})
	profile, ok := catalog.Lookup("fixture")
	if !ok || !profile.Accepts("none") || profile.Accepts("openai_hosted") {
		t.Fatal("fixture qualification was not selected")
	}
	snapshot := Snapshot{Agent: v1.Agent{Model: "fixture", Tools: []json.RawMessage{json.RawMessage(`{"type":"function","name":"echo","parameters":{"type":"object"}}`)}}, Environment: &v1.Environment{Type: "none"}}
	if err := validateProfileConfiguration(profile, snapshot); err != nil || !configurationChecked || !toolsChecked {
		t.Fatal("additional engine admission failed", err)
	}
	snapshot.Agent.Model = "invalid"
	if err := validateProfileConfiguration(profile, snapshot); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("profile configuration lost public error mapping", err)
	}
	inputs := []store.Input{{Kind: "tool_result", Payload: json.RawMessage(`{"call_id":"call","result":{"success":true,"output":"response"}}`)}}
	if err := validateProfileInputs(profile, "none", inputs); !errors.Is(err, store.ErrInvalidInput) || !resultChecked {
		t.Fatal("profile result lost public error mapping", err)
	}
}

func TestClaudeHostedToolsRequireSeparateQualification(t *testing.T) {
	for index, tools := range []string{`[{"type":"function","name":"f","parameters":{"type":"object"},"defer_loading":false}]`, `[{"type":"mcp","server_label":"s","transport":{"type":"http","server_url":"https://example.test/mcp"},"connection_origin":"service"}]`} {
		for _, placement := range []string{"none", "openai_hosted"} {
			raw := json.RawMessage(`{"agent":{"model":"fixture","tools":` + tools + `},"environment":{"type":"` + placement + `"}}`)
			if err := (Policy{}).ValidateSessionConfiguration("claude_sdk", raw); (err == nil) != (placement == "none" || index == 0) {
				t.Fatalf("%s: %v", placement, err)
			}
		}
	}
}

func TestExplicitValidationPoliciesPreserveErrorPrecedence(t *testing.T) {
	snapshot := Snapshot{Agent: v1.Agent{Tools: []json.RawMessage{json.RawMessage(`{"type":"unknown"}`)}}}
	_, decodeErr := executionTools(snapshot.Agent.Tools)
	if decodeErr == nil {
		t.Fatal("fixture must fail common tool decoding")
	}
	configurationErr := errors.New("configuration restriction")
	toolsErr := errors.New("tools restriction")
	for _, configuration := range []engine.ValidationPolicy{engine.CommonValidationOnly, engine.AdditionalValidation} {
		for _, rejectConfiguration := range []bool{false, true} {
			if configuration == engine.CommonValidationOnly && rejectConfiguration {
				continue
			}
			t.Run(fmt.Sprintf("configuration=%d/reject=%t", configuration, rejectConfiguration), func(t *testing.T) {
				toolsCalled := false
				profile := enginetest.Profile(func(p *engine.Profile) {
					p.ConfigurationValidation = configuration
					if configuration == engine.AdditionalValidation {
						p.ValidateConfiguration = func(v1.Agent, *v1.Environment, bool) error {
							if rejectConfiguration {
								return configurationErr
							}
							return nil
						}
					}
					p.ToolsValidation = engine.AdditionalValidation
					p.ValidateTools = func(*v1.Environment, bool, []proto.FunctionTool, []proto.MCPHTTPServer) error {
						toolsCalled = true
						return toolsErr
					}
				})
				catalog := engine.NewCatalog(map[string]engine.Profile{"fixture": profile})
				profile, _ = catalog.Lookup("fixture")
				err := validateProfileConfiguration(profile, snapshot)
				switch {
				case rejectConfiguration:
					if !errors.Is(err, configurationErr) || toolsCalled {
						t.Fatal("configuration lost precedence", err)
					}
				case configuration == engine.AdditionalValidation:
					if err == nil || err.Error() != decodeErr.Error() || toolsCalled {
						t.Fatal("decoding lost precedence", err)
					}
				default:
					if !errors.Is(err, toolsErr) || !toolsCalled {
						t.Fatal("common-only configuration changed tool error precedence", err)
					}
				}
			})
		}
	}
}

func TestCommonOnlyValidationPreservesFunctionResults(t *testing.T) {
	profile := enginetest.Profile(nil)
	catalog := engine.NewCatalog(map[string]engine.Profile{"fixture": profile})
	profile, _ = catalog.Lookup("fixture")
	inputs := []store.Input{{Kind: "tool_result", Payload: json.RawMessage(`{"call_id":"call","result":{"success":true,"output":"response"}}`)}}
	if err := validateProfileInputs(profile, "none", inputs); err != nil {
		t.Fatal("common-only result acquired a native restriction", err)
	}
}
