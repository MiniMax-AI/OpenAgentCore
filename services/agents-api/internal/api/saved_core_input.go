package api

import (
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig/builtin"
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
		{"harness", shape{kind: enumValue, values: builtin.Kinds()}},
		{"model_provider", nullableModelProviderShape()},
		{"harness_config", shape{kind: openObject}},
	}}
	if !json.Valid(extension) || checkValue("x_agents_core", extension, coreShape) != nil {
		return errors.New("x_agents_core must contain an optional supported harness and an optional complete model_provider input and native harness_config object.")
	}
	return nil
}

func nullableModelProviderShape() shape {
	result := modelProviderInputShape
	result.nullable = true
	return result
}
