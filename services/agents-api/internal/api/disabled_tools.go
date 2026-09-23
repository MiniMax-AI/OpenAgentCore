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

// webSearchTool is the resolved web_search projection. A present location
// projects all four keys; null and empty allowed_domains stay distinct.
type webSearchTool struct {
	Type           string    `json:"type"`
	Mode           *string   `json:"mode"`
	ContextSize    *string   `json:"context_size"`
	AllowedDomains []*string `json:"allowed_domains"`
	Location       *struct {
		City     *string `json:"city"`
		Country  *string `json:"country"`
		Region   *string `json:"region"`
		Timezone *string `json:"timezone"`
	} `json:"location"`
}

func decodeWebSearch(raw json.RawMessage) (webSearchTool, bool) {
	var tool webSearchTool
	return tool, decodeInputObject(raw, &tool, "type", "mode", "context_size", "allowed_domains", "location") == nil
}

// Optional settings are resource data in every mode; they never enable execution.
func (tool webSearchTool) resolveSettings() (json.RawMessage, error) {
	if tool.ContextSize == nil {
		value := "medium"
		tool.ContextSize = &value
	}
	for _, domain := range tool.AllowedDomains {
		if domain == nil {
			return nil, errors.New("web_search.allowed_domains must contain strings.")
		}
	}
	switch *tool.ContextSize {
	case "low", "medium", "high":
	default:
		return nil, errors.New("Invalid web_search.context_size.")
	}
	return json.Marshal(tool)
}

// Saved Agents keep every pinned mode, as the official service does; an omitted
// or null mode is saved as live. Session admission still qualifies only disabled
// search (resolveDisabledWebSearch), so saving never enables execution.
func resolveSavedWebSearch(raw json.RawMessage) (json.RawMessage, error) {
	tool, ok := decodeWebSearch(raw)
	if !ok {
		return nil, errors.New("Invalid web_search fields.")
	}
	if tool.Mode == nil {
		value := "live"
		tool.Mode = &value
	}
	switch *tool.Mode {
	case "disabled", "cached", "live":
	default:
		return nil, errors.New("web_search.mode must be disabled, cached or live.")
	}
	return tool.resolveSettings()
}

// Only disabled search is qualified for execution.
func resolveDisabledWebSearch(raw json.RawMessage) (json.RawMessage, error) {
	tool, ok := decodeWebSearch(raw)
	if !ok || tool.Mode == nil || *tool.Mode != "disabled" {
		return nil, errors.New("Only disabled web_search is qualified for execution.")
	}
	return tool.resolveSettings()
}
