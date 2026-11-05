package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// attachExclusive lists, in the pinned schema's order, the members the schema's
// `not` constraint forbids beside environment_id.
var attachExclusive = []string{"packages", "setup_commands", "network", "desktop", "env", "capability_directories", "skills", "plugins", "files", "environment_template_id", "container_size"}

// attachedEnvironment reads the standalone Environment a Session create names
// with environment.environment_id. Only type may accompany it, and presence
// counts, so an explicit null does too. The extension carries no preparation
// either. It returns "" when the request names none.
func attachedEnvironment(fields map[string]json.RawMessage, extension *v1.SessionExecutionInput) (string, error) {
	value, supplied := fields["environment_id"]
	if !supplied {
		return "", nil
	}
	for _, name := range attachExclusive {
		if _, present := fields[name]; present {
			return "", &fieldError{param: "environment." + name, message: fmt.Sprintf("environment.%s cannot be combined with environment.environment_id.", name)}
		}
	}
	// Any other member is unknown beside environment_id; report the first in
	// name order so the answer is deterministic.
	var unknown []string
	for name := range fields {
		if name != "type" && name != "environment_id" && !slices.Contains(attachExclusive, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		name := slices.Min(unknown)
		if !echoableField(name) {
			return "", errUnknownParameter
		}
		return "", &fieldError{param: "environment." + name, message: "Unknown parameter: 'environment." + name + "'."}
	}
	if extension != nil && !isNull(extension.Environment) {
		return "", &fieldError{param: "x_agents_core.environment", message: "x_agents_core.environment cannot be combined with environment.environment_id."}
	}
	if err := checkValue("environment.environment_id", value, environmentIDShape); err != nil {
		return "", err
	}
	var kind, id string
	_ = json.Unmarshal(fields["type"], &kind)
	_ = json.Unmarshal(value, &id)
	if kind != "openai_hosted" {
		return "", sessions.ErrInvalidInput
	}
	if n := utf8.RuneCountInString(id); n < 9 || n > 256 {
		return "", &fieldError{param: "environment.environment_id", message: fmt.Sprintf("Invalid 'environment.environment_id': string length must be between 9 and 256 characters, but got a string with length %d instead.", n)}
	}
	return id, nil
}
