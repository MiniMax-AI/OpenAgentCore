package api

import (
	"encoding/json"
	"errors"
)

// Validate presence before decoding: a supplied empty/null harness is invalid,
// and a provider replacement needs the complete write-only credential bundle.
// Extension errors never echo user values or unknown member names.
func validateSavedCoreInput(raw []byte) error {
	_, fields := orderedMembers(raw)
	extension, supplied := fields["x_agents_core"]
	if !supplied {
		return nil
	}
	coreShape := shape{kind: objectValue, nullable: true, members: []member{
		{"harness", shape{kind: enumValue, values: []string{"codex", "claude_sdk", "mcode"}}},
		{"model_provider", shape{kind: objectValue, nullable: true, members: []member{
			{"protocol", requiredString}, {"base_url", requiredString}, {"api_key", requiredString},
			{"context_window", shape{kind: integerValue, minimum: 0}},
			{"max_output_tokens", shape{kind: integerValue, minimum: 0}},
		}}},
	}}
	if !json.Valid(extension) || checkValue("x_agents_core", extension, coreShape) != nil {
		return errors.New("x_agents_core must contain an optional supported harness and an optional complete model_provider input.")
	}
	return nil
}
