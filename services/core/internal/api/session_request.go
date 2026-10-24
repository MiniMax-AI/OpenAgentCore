package api

import (
	"bytes"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Keep explicit null until validation for fields whose Go zero values would
// otherwise erase it. The embedded wire type retains strict nested decoding.
type decodedSessionRequest struct {
	v1.CreateSessionRequest
	Execution   json.RawMessage `json:"x_agents_core"`
	Input       json.RawMessage `json:"input"`
	Agent       json.RawMessage `json:"agent"`
	Environment json.RawMessage `json:"environment"`
	VaultIDs    []string        `json:"vault_ids"`
}

type sessionRequest struct {
	initialFiles          []environmentconfig.InitialFile
	initialization        environmentconfig.Setup
	originalEnvironment   json.RawMessage
	modelProviderNull     bool
	deploymentDefaults    *modelconfiguration.Snapshot
	modelSource           v1.ExecutionSource
	harnessConfigSource   v1.ExecutionSource
	resolvedHarnessConfig json.RawMessage
	v1.CreateSessionRequest
	Input               json.RawMessage
	templateID          string
	templateEnvironment json.RawMessage
	agentFields         map[string]json.RawMessage
}

func (request decodedSessionRequest) validated() (sessionRequest, error) {
	input := sessionRequest{CreateSessionRequest: request.CreateSessionRequest, Input: request.Input}
	if len(request.Execution) > 0 && !bytes.Equal(bytes.TrimSpace(request.Execution), []byte("null")) {
		if decodeInputObject(request.Execution, &input.XAgentsCore, "model_provider", "harness_config", "environment") != nil {
			return input, sessions.ErrInvalidInput
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(request.Execution, &fields)
		input.modelProviderNull = bytes.Equal(bytes.TrimSpace(fields["model_provider"]), []byte("null"))
	}
	input.VaultIDs = append([]string{}, request.VaultIDs...)
	canonicalEnvironment, err := preparationEnvironmentInput(request.Environment, input.XAgentsCore)
	if err != nil {
		return input, err
	}
	input.originalEnvironment = request.Environment
	var environmentFields map[string]json.RawMessage
	if json.Unmarshal(canonicalEnvironment, &environmentFields) != nil {
		return input, sessions.ErrInvalidInput
	}
	input.initialization, err = decodeEnvironmentSetup(environmentFields)
	if err != nil {
		return input, err
	}
	input.initialFiles, err = decodeInitialFiles(environmentFields["files"])
	if err != nil {
		return input, err
	}
	input.Environment, input.templateID, input.templateEnvironment, err = decodePreparationTemplate(canonicalEnvironment, true)
	if err != nil {
		return input, err
	}
	if len(request.Agent) > 0 {
		// Protocol errors in the inline agent precede the input requirement.
		if err := validateSessionAgent(request.Agent); err != nil {
			return input, err
		}
		if decodeInputObject(request.Agent, &input.Agent, "model", "instructions", "multi_agent", "reasoning", "service_tier", "text", "tools", "x_agents_core") != nil {
			return input, sessions.ErrInvalidInput
		}
		if err := json.Unmarshal(request.Agent, &input.agentFields); err != nil {
			return input, sessions.ErrInvalidInput
		}
	}
	input.Metadata = metadataOrEmpty(request.Metadata)
	return input, nil
}
