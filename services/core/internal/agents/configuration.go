package agents

import (
	"encoding/json"
	"fmt"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
)

// encodeMetadata encodes caller metadata for storage; nil is the empty object.
func encodeMetadata(values map[string]string) (json.RawMessage, error) {
	encoded, err := metadata.Encode(values)
	if err != nil {
		return nil, fmt.Errorf("%w: metadata: %v", ErrInvalidInput, err)
	}
	return encoded, nil
}

// createConfiguration normalizes the configuration of a new Agent.
func createConfiguration(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaxConfigurationBytes {
		return nil, fmt.Errorf("%w: configuration must be an object of at most 512 KiB", ErrInvalidInput)
	}
	return normalizeConfiguration(raw)
}

// decodePatch normalizes an update's supplied fields; empty supplies none.
func decodePatch(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > MaxConfigurationBytes {
		return nil, fmt.Errorf("%w: configuration patch exceeds 512 KiB", ErrInvalidInput)
	}
	normalized, err := normalizeConfiguration(raw)
	if err != nil {
		return nil, err
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &patch); err != nil {
		return nil, fmt.Errorf("%w: configuration patch: %v", ErrInvalidInput, err)
	}
	return patch, nil
}

func normalizeConfiguration(raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := jsonobject.Normalize(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: configuration: %v", ErrInvalidInput, err)
	}
	return normalized, nil
}

// validateModelExecution checks the saved Harness configuration and that the
// model provider bundle suits the saved Harness. provider is the bundle being
// saved, if any.
func validateModelExecution(configuration json.RawMessage, provider *v1.ModelProviderInput) error {
	if provider != nil {
		if err := provider.Validate(); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidInput, err)
		}
	}
	var config struct {
		Core *v1.SavedAgentCore `json:"x_agents_core"`
	}
	if err := json.Unmarshal(configuration, &config); err != nil {
		return fmt.Errorf("%w: x_agents_core: %v", ErrInvalidInput, err)
	}
	if config.Core != nil {
		if err := v1.ValidateHarnessConfig(config.Core.Harness, config.Core.HarnessConfig); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidInput, err)
		}
	}
	if config.Core == nil || config.Core.ModelProvider == nil || config.Core.Harness == "" {
		return nil
	}
	if err := config.Core.ModelProvider.ValidateHarness(config.Core.Harness); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidInput, err)
	}
	return nil
}

// mergeConfiguration applies an update's supplied fields to the saved
// configuration. Supplied top-level fields replace saved ones; x_agents_core
// subfields merge one by one, and a null x_agents_core removes it. Changing the
// model, the model provider or the Harness without supplying harness_config
// resets harness_config to {}, because native options belong to one model and
// Harness.
func mergeConfiguration(saved json.RawMessage, patch map[string]json.RawMessage) (json.RawMessage, error) {
	configuration := map[string]json.RawMessage{}
	if err := json.Unmarshal(saved, &configuration); err != nil {
		return nil, fmt.Errorf("decode saved Agent configuration: %w", err)
	}
	coreNull := string(patch["x_agents_core"]) == "null"
	var corePatch map[string]json.RawMessage
	if raw := patch["x_agents_core"]; len(raw) > 0 && !coreNull {
		if err := json.Unmarshal(raw, &corePatch); err != nil {
			return nil, fmt.Errorf("%w: x_agents_core: %v", ErrInvalidInput, err)
		}
	}
	_, modelChanged := patch["model"]
	_, providerChanged := corePatch["model_provider"]
	_, harnessChanged := corePatch["harness"]
	_, nativeSupplied := corePatch["harness_config"]
	if (modelChanged || providerChanged || harnessChanged) && !nativeSupplied && !coreNull {
		if corePatch == nil {
			corePatch = map[string]json.RawMessage{}
		}
		corePatch["harness_config"] = json.RawMessage(`{}`)
	}
	for field, value := range patch {
		if field != "x_agents_core" {
			configuration[field] = value
		}
	}
	switch {
	case coreNull:
		configuration["x_agents_core"] = patch["x_agents_core"]
	case corePatch != nil:
		core := map[string]json.RawMessage{}
		if old := configuration["x_agents_core"]; len(old) != 0 && string(old) != "null" {
			if err := json.Unmarshal(old, &core); err != nil {
				return nil, fmt.Errorf("decode saved Agent configuration: %w", err)
			}
		}
		for key, replacement := range corePatch {
			core[key] = replacement
		}
		merged, err := json.Marshal(core)
		if err != nil {
			return nil, err
		}
		configuration["x_agents_core"] = merged
	}
	merged, err := json.Marshal(configuration)
	if err != nil {
		return nil, err
	}
	merged, err = normalizeConfiguration(merged)
	if err != nil {
		return nil, err
	}
	if len(merged) > MaxConfigurationBytes {
		return nil, fmt.Errorf("%w: configuration exceeds 512 KiB", ErrInvalidInput)
	}
	return merged, nil
}
