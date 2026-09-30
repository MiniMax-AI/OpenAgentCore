package execution

import (
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func profileError(err error) error {
	if errors.Is(err, engine.ErrInvalidInput) {
		return store.ErrInvalidInput
	}
	return err
}

func validateProfileConfiguration(profile engine.Profile, snapshot Snapshot) error {
	if snapshot.Agent.Text.Format.Type == "json_schema" && !profile.StructuredOutput {
		return errors.New("Structured output is not qualified for this engine.")
	}
	if profile.ValidateConfiguration != nil {
		if err := profile.ValidateConfiguration(snapshot.Agent, snapshot.Environment, snapshot.Daemon != nil); err != nil {
			return profileError(err)
		}
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	// Preserve each profile's admission error precedence when tool decoding fails.
	if profile.ValidateConfiguration != nil && err != nil {
		return err
	}
	if err == nil {
		if err := profile.ValidateMCPOrigins(snapshot.Environment, snapshot.Daemon != nil, tools.MCP); err != nil {
			return err
		}
		if tools.DisableProgrammatic && !profile.ProgrammaticToolCallingDisable {
			return errors.New("Disabling programmatic tool calling is not qualified for this engine.")
		}
		request := proto.PromptRequestPayload{ToolSearch: tools.Search, FunctionTools: tools.Functions}
		if err := request.ValidateToolSearch(profile.ToolSearch); err != nil {
			return err
		}
	}
	if profile.ValidateTools != nil {
		if validationErr := profile.ValidateTools(snapshot.Environment, snapshot.Daemon != nil, tools.Functions, tools.MCP); validationErr != nil {
			return profileError(validationErr)
		}
	}
	return err
}

func validateProfileInputs(profile engine.Profile, placement string, inputs []store.Input) error {
	for _, input := range inputs {
		if input.Kind == "message" {
			messages, err := messageInput(input.Payload)
			if err != nil {
				return err
			}
			if err := validateMessageImageProfile(profile, placement, messages); err != nil {
				return err
			}
			if err := validateMessageTextProfile(profile, messages); err != nil {
				return err
			}
			continue
		}
		if input.Kind != "tool_result" || profile.ValidateFunctionResult == nil {
			continue
		}
		var value store.FunctionResultInput
		if json.Unmarshal(input.Payload, &value) != nil {
			return store.ErrInvalidInput
		}
		result, err := functionResult(store.FunctionCall{CallID: value.CallID, Result: value.Result})
		if err != nil {
			return store.ErrInvalidInput
		}
		if err := profile.ValidateFunctionResult(placement, result); err != nil {
			return profileError(err)
		}
	}
	return nil
}
