package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
)

// resolveSavedAgent returns the command without its tenant.
func resolveSavedAgent(input v1.CreateAgentRequest) (agents.CreateCommand, error) {
	if input.Model == nil {
		return agents.CreateCommand{}, errors.New("model is required and must be a string.")
	}
	return resolveSavedFields(input)
}

// Update requests reuse field validation without requiring an omitted model.
func resolveSavedFields(input v1.CreateAgentRequest) (agents.CreateCommand, error) {
	if input.Name != nil {
		if length := utf8.RuneCountInString(*input.Name); length > 128 {
			return agents.CreateCommand{}, &fieldError{param: "name", message: fmt.Sprintf("Invalid 'name': string too long. Expected a string with maximum length 128, but got a string with length %d instead.", length)}
		}
	}
	values, err := stringMetadata(input.Metadata)
	if err != nil {
		return agents.CreateCommand{}, err
	}
	if err := metadataFieldError(metadata.Validate(values)); err != nil {
		return agents.CreateCommand{}, err
	}
	if err := input.XAgentsCore.Validate(); err != nil {
		return agents.CreateCommand{}, err
	}
	cfg := v1.SavedAgentConfiguration{XAgentsCore: input.XAgentsCore.SafeView(), Name: input.Name, Instructions: input.Instructions, ServiceTier: "auto"}
	if input.Model != nil {
		cfg.Model = *input.Model
	}
	cfg.MultiAgent, err = resolveSavedMultiAgent(input.MultiAgent)
	if err != nil {
		return agents.CreateCommand{}, err
	}
	if input.ServiceTier != nil {
		cfg.ServiceTier = *input.ServiceTier
	}
	if input.Reasoning != nil {
		cfg.Reasoning = *input.Reasoning
	}
	// Model-derived effort resolution is a recorded gap. Do not manufacture a
	// default from the operator's execution engine or another model's catalog.
	cfg.Text, err = resolveText(input.Text)
	if err != nil {
		return agents.CreateCommand{}, err
	}
	cfg.Tools, err = resolveSavedTools(input.Tools)
	if err != nil {
		return agents.CreateCommand{}, err
	}
	cfg.Compaction, cfg.Experimental, err = resolveAgentOptions(input.Compaction, input.Experimental)
	if err != nil {
		return agents.CreateCommand{}, err
	}
	configuration, err := json.Marshal(cfg)
	result := agents.CreateCommand{Metadata: values, Configuration: configuration}
	if input.XAgentsCore != nil {
		result.ModelProvider = input.XAgentsCore.ModelProvider
	}
	return result, err
}

// resolveAgentOptions stores compaction only when supplied; an object without a value
// takes the official default of background compaction. Experimental always carries its
// official default, time awareness off. Whether a Harness runs them is decided at Session admission.
func resolveAgentOptions(compaction, experimental json.RawMessage) (resolvedCompaction, resolvedExperimental json.RawMessage, err error) {
	if !isNull(compaction) {
		input := struct {
			Async *bool `json:"async"`
		}{}
		if err = json.Unmarshal(compaction, &input); err != nil {
			return nil, nil, &storedDataError{err}
		}
		async := input.Async == nil || *input.Async
		resolvedCompaction, _ = json.Marshal(struct {
			Async bool `json:"async"`
		}{async})
	}
	input := struct {
		TimeAwareness bool `json:"time_awareness"`
	}{}
	if !isNull(experimental) {
		if err = json.Unmarshal(experimental, &input); err != nil {
			return nil, nil, &storedDataError{err}
		}
	}
	resolvedExperimental, _ = json.Marshal(input)
	return resolvedCompaction, resolvedExperimental, nil
}

// resolveSavedMultiAgent and resolveText read values whose pinned shape was
// checked at decode; only the uint32 bound of max_concurrent_subagents remains.
func resolveSavedMultiAgent(raw json.RawMessage) (v1.MultiAgentConfig, error) {
	result := v1.MultiAgentConfig{}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return result, nil
	}
	input := struct {
		Enabled bool   `json:"enabled"`
		Max     uint32 `json:"max_concurrent_subagents"`
	}{Max: 6}
	if json.Unmarshal(raw, &input) != nil {
		return result, errors.New("max_concurrent_subagents must be an integer from 1 to 4294967295.")
	}
	result.Enabled = input.Enabled
	if result.Enabled {
		value := int(input.Max)
		result.MaxConcurrentSubagents = &value
	}
	return result, nil
}

func resolveText(input *v1.TextConfigInput) (v1.TextConfig, error) {
	result := v1.TextConfig{Format: v1.TextFormat{Type: "text"}, Verbosity: "medium"}
	if input == nil {
		return result, nil
	}
	if input.Verbosity != nil {
		result.Verbosity = *input.Verbosity
	}
	if len(input.Format) == 0 || bytes.Equal(bytes.TrimSpace(input.Format), []byte("null")) {
		return result, nil
	}
	if err := json.Unmarshal(input.Format, &result.Format); err != nil {
		return result, &storedDataError{err}
	}
	return result, nil
}
