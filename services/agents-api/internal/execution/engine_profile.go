package execution

import (
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
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
	functions, mcp, search, err := executionTools(snapshot.Agent.Tools)
	// Preserve each profile's admission error precedence when tool decoding fails.
	if profile.ValidateConfiguration != nil && err != nil {
		return err
	}
	if err == nil {
		request := proto.PromptRequestPayload{ToolSearch: search, FunctionTools: functions}
		if err := request.ValidateToolSearch(profile.ToolSearch); err != nil {
			return err
		}
	}
	if profile.ValidateTools != nil {
		if validationErr := profile.ValidateTools(snapshot.Environment, snapshot.Daemon != nil, functions, mcp); validationErr != nil {
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
		if err := profile.ValidateFunctionResult(result.Content); err != nil {
			return profileError(err)
		}
	}
	return nil
}
