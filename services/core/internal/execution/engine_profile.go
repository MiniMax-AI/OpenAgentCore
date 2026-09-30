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
	if snapshot.Agent.Text.Format.Type == "json_schema" && !profile.StructuredOutput.IsSupported() {
		return errors.New("Structured output is not qualified for this engine.")
	}
	if profile.ConfigurationValidation == engine.AdditionalValidation {
		if err := profile.ValidateConfiguration(snapshot.Agent, snapshot.Environment); err != nil {
			return profileError(err)
		}
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	// Preserve each profile's admission error precedence when tool decoding fails.
	if profile.ConfigurationValidation == engine.AdditionalValidation && err != nil {
		return err
	}
	if err == nil {
		if err := profile.ValidateMCPOrigins(snapshot.Environment, tools.MCP); err != nil {
			return err
		}
		if tools.DisableProgrammatic && !profile.ProgrammaticToolCallingDisable.IsSupported() {
			return errors.New("Disabling programmatic tool calling is not qualified for this engine.")
		}
		request := proto.PromptRequestPayload{ToolSearch: tools.Search, FunctionTools: tools.Functions}
		if err := request.ValidateToolSearch(profile.ToolSearch.IsSupported()); err != nil {
			return err
		}
	}
	if profile.ToolsValidation == engine.AdditionalValidation {
		if validationErr := profile.ValidateTools(snapshot.Environment, tools.Functions, tools.MCP); validationErr != nil {
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
		if input.Kind != "tool_result" || profile.FunctionResultValidation == engine.CommonValidationOnly {
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
