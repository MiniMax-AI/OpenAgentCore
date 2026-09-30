package api

import (
	"bytes"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// The official placement union and the Core extension converge before resource
// resolution. All confidential fields use the existing encrypted setup snapshot.
func preparationEnvironmentInput(raw json.RawMessage, extension *v1.SessionExecutionInput) (json.RawMessage, error) {
	// Validate the pinned input before admitting any extension fields.
	if _, _, _, err := decodeTemplateEnvironment(raw); err != nil {
		return nil, err
	}
	if extension == nil || len(extension.Environment) == 0 || bytes.Equal(bytes.TrimSpace(extension.Environment), []byte("null")) {
		return raw, nil
	}
	var fields, setup map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(extension.Environment, &setup) != nil || setup == nil {
		return nil, sessions.ErrInvalidInput
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil || (kind != "openai_hosted" && kind != "self_hosted") {
		return nil, sessions.ErrInvalidInput
	}
	for name, value := range setup {
		switch name {
		case "environment_template_id", "files", "env", "packages", "setup_commands", "skills", "plugins", "capability_directories":
		default:
			return nil, sessions.ErrInvalidInput
		}
		// Duplicate sources are ambiguous, including an explicit null.
		if _, exists := fields[name]; exists {
			return nil, sessions.ErrInvalidInput
		}
		fields[name] = value
	}
	return json.Marshal(fields)
}
