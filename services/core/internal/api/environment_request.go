package api

import (
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func decodeSessionEnvironment(raw json.RawMessage) (*v1.Environment, error) {
	var environment v1.Environment
	if json.Unmarshal(raw, &environment) != nil {
		return nil, sessions.ErrInvalidInput
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil {
		return nil, sessions.ErrInvalidInput
	}
	if err := rejectSystemPackages(input["packages"]); err != nil {
		return nil, err
	}
	fields := []string{"type"}
	switch environment.Type {
	case "none":
	case "openai_hosted":
		return decodeHostedEnvironment(raw)
	case "self_hosted":
		fields = append(fields, "workspace_directory", "capability_directories")
		if agentcapabilities.ValidateSourceDirectories([]string{environment.WorkspaceDirectory}) != nil || agentcapabilities.ValidateSourceDirectories(environment.CapabilityDirectories) != nil {
			return nil, sessions.ErrInvalidInput
		}
	default:
		return nil, sessions.ErrInvalidInput
	}
	if err := decodeInputObject(raw, &environment, fields...); err != nil {
		return nil, err
	}
	return &environment, nil
}
