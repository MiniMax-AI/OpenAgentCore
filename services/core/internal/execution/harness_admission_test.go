package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/harnesstest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
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

func TestAdditionalDeclarationUsesCommonAdmission(t *testing.T) {
	configurationChecked, toolsChecked, resultChecked := false, false, false
	declaration := harnesstest.Declaration("fixture", func(d *harnessconfig.Declaration) {
		d.ValidateConfiguration = func(admission harnessconfig.Admission) error {
			configurationChecked = true
			if admission.Model != "fixture" || admission.Environment == nil {
				return harnessconfig.ErrInvalidInput
			}
			return nil
		}
		d.ValidateTools = func(tools []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
			toolsChecked = true
			if len(tools) != 1 || tools[0].Name != "echo" || len(mcp) != 0 {
				t.Fatal("common tool decoding did not reach the declaration")
			}
			return nil
		}
		d.ValidateFunctionResult = func(result proto.FunctionResultPayload) error {
			content := result.Content
			resultChecked = true
			if !result.Success || len(content) != 1 || content[0].Text == nil || *content[0].Text != "response" {
				t.Fatal("common result decoding did not reach the declaration")
			}
			return harnessconfig.ErrInvalidInput
		}
	})
	registry := harnessconfig.NewRegistry(declaration)
	declaration, ok := registry.Lookup("fixture")
	if !ok || !declaration.Accepts("none") || declaration.Accepts("openai_hosted") {
		t.Fatal("fixture declaration was not selected")
	}
	snapshot := Snapshot{Agent: v1.Agent{Model: "fixture", Tools: []json.RawMessage{json.RawMessage(`{"type":"function","name":"echo","parameters":{"type":"object"}}`)}}, Environment: &v1.Environment{Type: "none"}}
	if err := validateDeclaredConfiguration(declaration, snapshot); err != nil || !configurationChecked || !toolsChecked {
		t.Fatal("additional engine admission failed", err)
	}
	snapshot.Agent.Model = "invalid"
	if err := validateDeclaredConfiguration(declaration, snapshot); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("declared configuration lost public error mapping", err)
	}
	inputs := []sessions.Input{{Kind: "tool_result", Payload: json.RawMessage(`{"call_id":"call","result":{"success":true,"output":"response"}}`)}}
	if err := validateDeclaredInputs(declaration, inputs); !errors.Is(err, sessions.ErrInvalidInput) || !resultChecked {
		t.Fatal("declared result lost public error mapping", err)
	}
}

func TestClaudeHostedToolsRequireSeparateSupport(t *testing.T) {
	for index, tools := range []string{`[{"type":"function","name":"f","parameters":{"type":"object"},"defer_loading":false}]`, `[{"type":"mcp","server_label":"s","transport":{"type":"http","server_url":"https://example.test/mcp"},"connection_origin":"service"}]`} {
		for _, placement := range []string{"none", "openai_hosted"} {
			raw := json.RawMessage(`{"agent":{"model":"fixture","tools":` + tools + `},"environment":{"type":"` + placement + `"}}`)
			if err := (Policy{}).ValidateSessionConfiguration("claude_sdk", raw); (err == nil) != (placement == "none" || index == 0) {
				t.Fatalf("%s: %v", placement, err)
			}
		}
	}
}

func TestValidatorPrecedencePreservesErrors(t *testing.T) {
	snapshot := Snapshot{Agent: v1.Agent{Tools: []json.RawMessage{json.RawMessage(`{"type":"unknown"}`)}}}
	_, decodeErr := executionTools(snapshot.Agent.Tools)
	if decodeErr == nil {
		t.Fatal("fixture must fail common tool decoding")
	}
	configurationErr := errors.New("configuration restriction")
	toolsErr := errors.New("tools restriction")
	for _, hasConfiguration := range []bool{false, true} {
		for _, rejectConfiguration := range []bool{false, true} {
			if !hasConfiguration && rejectConfiguration {
				continue
			}
			t.Run(fmt.Sprintf("configuration=%t/reject=%t", hasConfiguration, rejectConfiguration), func(t *testing.T) {
				toolsCalled := false
				declaration := harnesstest.Declaration("fixture", func(d *harnessconfig.Declaration) {
					if hasConfiguration {
						d.ValidateConfiguration = func(harnessconfig.Admission) error {
							if rejectConfiguration {
								return configurationErr
							}
							return nil
						}
					}
					d.ValidateTools = func([]proto.FunctionTool, []proto.MCPHTTPServer) error {
						toolsCalled = true
						return toolsErr
					}
				})
				err := validateDeclaredConfiguration(declaration, snapshot)
				switch {
				case rejectConfiguration:
					if !errors.Is(err, configurationErr) || toolsCalled {
						t.Fatal("configuration lost precedence", err)
					}
				case hasConfiguration:
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
	declaration := harnesstest.Declaration("fixture", nil)
	inputs := []sessions.Input{{Kind: "tool_result", Payload: json.RawMessage(`{"call_id":"call","result":{"success":true,"output":"response"}}`)}}
	if err := validateDeclaredInputs(declaration, inputs); err != nil {
		t.Fatal("common-only result acquired a native restriction", err)
	}
}

func TestRetainedNativeHistoryDependsOnExternalWorkspace(t *testing.T) {
	for _, required := range []bool{false, true} {
		for _, supported := range []bool{false, true} {
			if err := sessions.ValidateRetainedHistory(required, supported); (err == nil) != (!required || supported) {
				t.Fatal(required, supported, err)
			}
		}
	}
	for _, declared := range []bool{false, true} {
		declaration := harnesstest.Declaration("test_harness", func(d *harnessconfig.Declaration) {
			d.Placements = []string{"openai_hosted"}
			d.Capabilities.RetainedNativeHistory = proto.CapabilityFromBool(declared)
		})
		policy := Policy{Declarations: harnessconfig.NewRegistry(declaration)}
		if err := policy.ValidateSessionConfiguration("test_harness", json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"openai_hosted"}}`)); err != nil {
			t.Fatal("generic hosted configuration acquired a storage requirement", declared, err)
		}
		for _, external := range []bool{false, true} {
			for _, runtimeSupported := range []bool{false, true} {
				err := policy.validateEnvironmentHistory("test_harness", sessions.Environment{ExternalWorkspace: external}, runtimeSupported)
				if (err == nil) != (!external || declared && runtimeSupported) {
					t.Fatal(external, declared, runtimeSupported, err)
				}
			}
		}
	}
}
