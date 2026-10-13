package api

import (
	"encoding/json"
	"errors"
)

// Tool resolvers read tools whose pinned shape was checked at the /v1 boundary:
// request tools, or a saved Agent's tools that resolveSavedTools stored. A
// decode failure is Core's own fault and is reported as a service error.
func resolveProgrammaticTool(raw json.RawMessage) (json.RawMessage, bool, error) {
	input := struct {
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}{Enabled: true}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, false, &storedDataError{err}
	}
	value, err := json.Marshal(input)
	return value, input.Enabled, err
}

// webSearchTool is the resolved web_search projection. A present location
// projects all four keys; null and empty allowed_domains stay distinct.
type webSearchTool struct {
	Type           string   `json:"type"`
	Mode           *string  `json:"mode"`
	ContextSize    *string  `json:"context_size"`
	AllowedDomains []string `json:"allowed_domains"`
	Location       *struct {
		City     *string `json:"city"`
		Country  *string `json:"country"`
		Region   *string `json:"region"`
		Timezone *string `json:"timezone"`
	} `json:"location"`
}

// Optional settings are resource data in every mode; they never enable execution.
func (tool webSearchTool) resolveSettings() (json.RawMessage, error) {
	if tool.ContextSize == nil {
		value := "medium"
		tool.ContextSize = &value
	}
	return json.Marshal(tool)
}

// Saved Agents keep every pinned mode, as the official service does; an omitted
// or null mode is saved as live. Session admission still qualifies only disabled
// search (resolveDisabledWebSearch), so saving never enables execution.
func resolveSavedWebSearch(raw json.RawMessage) (json.RawMessage, error) {
	var tool webSearchTool
	if err := json.Unmarshal(raw, &tool); err != nil {
		return nil, &storedDataError{err}
	}
	if tool.Mode == nil {
		value := "live"
		tool.Mode = &value
	}
	return tool.resolveSettings()
}

// Only disabled search is qualified for execution.
func resolveDisabledWebSearch(raw json.RawMessage) (json.RawMessage, error) {
	var tool webSearchTool
	if err := json.Unmarshal(raw, &tool); err != nil {
		return nil, &storedDataError{err}
	}
	if tool.Mode == nil || *tool.Mode != "disabled" {
		return nil, errors.New("Only disabled web_search is qualified for execution.")
	}
	return tool.resolveSettings()
}
