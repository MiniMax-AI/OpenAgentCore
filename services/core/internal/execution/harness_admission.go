package execution

import (
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func admissionError(err error) error {
	if errors.Is(err, harnessconfig.ErrInvalidInput) {
		return sessions.ErrInvalidInput
	}
	return err
}

// admissionEnvironment projects the public Environment into the view Harness
// declarations validate.
func admissionOf(snapshot Snapshot) harnessconfig.Admission {
	agent := snapshot.Agent
	workspace := snapshot.Environment.WorkspaceDirectory
	if snapshot.Environment.Type == "openai_hosted" {
		workspace = hostedWorkspaceDirectory
	}
	return harnessconfig.Admission{
		TextFormat:         agent.Text.Format.Type,
		TextSchema:         agent.Text.Format.Schema,
		MultiAgent:         agent.MultiAgent.Enabled,
		Tools:              agent.Tools,
		WorkspaceDirectory: workspace,
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
			return admissionError(err)
		}
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		return err
	}
	if err := declaration.ValidateMCPOrigins(snapshot.Environment.Type, tools.MCP); err != nil {
		return err
	}
	if err := declaration.ValidateExecutionControls(executionControls(snapshot.Agent, tools)); err != nil {
		return err
	}
	request := proto.PromptRequestPayload{ToolSearch: tools.Search, FunctionTools: tools.Functions}
	if err := request.ValidateToolSearch(declaration.Capabilities.ToolSearch.IsSupported()); err != nil {
		return err
	}
	if declaration.ValidateTools != nil {
		if err := declaration.ValidateTools(tools.Functions, tools.MCP); err != nil {
			return admissionError(err)
		}
	}
	return nil
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
		if input.Kind != "tool_result" {
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
		if !declaration.Capabilities.FunctionResultImages.IsSupported() && (proto.MessageInput{{Content: result.Content}}).HasImages() {
			return sessions.ErrInvalidInput
		}
		if declaration.ValidateFunctionResult != nil {
			if err := declaration.ValidateFunctionResult(result); err != nil {
				return admissionError(err)
			}
		}
	}
	return nil
}
