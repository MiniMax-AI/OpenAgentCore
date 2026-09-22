package api

import (
	"encoding/json"
	"errors"
)

func resolveProgrammaticTool(raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Type    string          `json:"type"`
		Enabled json.RawMessage `json:"enabled"`
	}
	if decodeInputObject(raw, &input, "type", "enabled") != nil {
		return nil, errors.New("Invalid programmatic_tool_calling fields.")
	}
	enabled, err := optionalBoolean(input.Enabled, true)
	if err != nil {
		return nil, errors.New("programmatic_tool_calling.enabled must be a boolean.")
	}
	return json.Marshal(struct {
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}{input.Type, enabled})
}

// Only disabled search is qualified; its optional settings remain resource data.
func resolveDisabledWebSearch(raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Type           string    `json:"type"`
		Mode           string    `json:"mode"`
		ContextSize    *string   `json:"context_size"`
		AllowedDomains []*string `json:"allowed_domains"`
		Location       *struct {
			City     *string `json:"city"`
			Country  *string `json:"country"`
			Region   *string `json:"region"`
			Timezone *string `json:"timezone"`
		} `json:"location"`
	}
	if decodeInputObject(raw, &input, "type", "mode", "context_size", "allowed_domains", "location") != nil || input.Mode != "disabled" {
		return nil, errors.New("Only disabled web_search is qualified for execution.")
	}
	if input.ContextSize == nil {
		value := "medium"
		input.ContextSize = &value
	}
	for _, domain := range input.AllowedDomains {
		if domain == nil {
			return nil, errors.New("web_search.allowed_domains must contain strings.")
		}
	}
	switch *input.ContextSize {
	case "low", "medium", "high":
	default:
		return nil, errors.New("Invalid web_search.context_size.")
	}
	return json.Marshal(input)
}
