package execution

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func profileError(err error) error {
	if errors.Is(err, harnessconfig.ErrInvalidInput) {
		return sessions.ErrInvalidInput
	}
	return err
}

// admissionEnvironment projects the public Environment into the view Harness
// declarations validate.
func admissionEnvironment(environment *v1.Environment) *harnessconfig.AdmissionEnvironment {
	if environment == nil {
		return nil
	}
	return &harnessconfig.AdmissionEnvironment{
		Type:       environment.Type,
		Extensions: len(environment.Skills) != 0 || len(environment.Plugins) != 0 || len(environment.CapabilityDirectories) != 0,
	}
}

func admissionOf(snapshot Snapshot) harnessconfig.Admission {
	agent := snapshot.Agent
	return harnessconfig.Admission{
		Model:         agent.Model,
		Reasoning:     agent.Reasoning.Effort != nil || agent.Reasoning.Summary != nil,
		ServiceTier:   agent.ServiceTier,
		TextVerbosity: agent.Text.Verbosity,
		TextFormat:    agent.Text.Format.Type,
		TextSchema:    agent.Text.Format.Schema,
		MultiAgent:    agent.MultiAgent.Enabled,
		Tools:         agent.Tools,
		Environment:   admissionEnvironment(snapshot.Environment),
	}
}

// validateDeclaredConfiguration applies the Harness declaration to a frozen
// Session snapshot. Core adds no restriction of its own per Harness.
func validateDeclaredConfiguration(declaration harnessconfig.Declaration, snapshot Snapshot) error {
	if snapshot.Agent.Text.Format.Type == "json_schema" && !declaration.Capabilities.StructuredOutput.IsSupported() {
		return errors.New("Structured output is not supported by this engine.")
	}
	if declaration.ValidateConfiguration != nil {
		if err := declaration.ValidateConfiguration(admissionOf(snapshot)); err != nil {
			return profileError(err)
		}
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	// Preserve each declaration's admission error precedence when tool decoding fails.
	if declaration.ValidateConfiguration != nil && err != nil {
		return err
	}
	if err == nil {
		if err := declaration.ValidateMCPOrigins(admissionEnvironment(snapshot.Environment), tools.MCP); err != nil {
			return err
		}
		if tools.DisableProgrammatic && !declaration.Capabilities.ProgrammaticToolCallingDisable.IsSupported() {
			return errors.New("Disabling programmatic tool calling is not supported by this engine.")
		}
		request := proto.PromptRequestPayload{ToolSearch: tools.Search, FunctionTools: tools.Functions}
		if err := request.ValidateToolSearch(declaration.Capabilities.ToolSearch.IsSupported()); err != nil {
			return err
		}
	}
	if declaration.ValidateTools != nil {
		if validationErr := declaration.ValidateTools(tools.Functions, tools.MCP); validationErr != nil {
			return profileError(validationErr)
		}
	}
	return err
}

func validateDeclaredInputs(declaration harnessconfig.Declaration, inputs []sessions.Input) error {
	for _, input := range inputs {
		if input.Kind == "message" {
			messages, err := messageInput(input.Payload)
			if err != nil {
				return err
			}
			if err := validateMessageImages(declaration, messages); err != nil {
				return err
			}
			if err := validateMessageText(declaration, messages); err != nil {
				return err
			}
			continue
		}
		if input.Kind != "tool_result" || declaration.ValidateFunctionResult == nil {
			continue
		}
		var value sessions.FunctionResultInput
		if json.Unmarshal(input.Payload, &value) != nil {
			return sessions.ErrInvalidInput
		}
		result, err := functionResult(sessions.FunctionCall{CallID: value.CallID, Result: value.Result})
		if err != nil {
			return sessions.ErrInvalidInput
		}
		if err := declaration.ValidateFunctionResult(result); err != nil {
			return profileError(err)
		}
	}
	return nil
}
