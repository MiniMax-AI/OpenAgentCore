package api

import "encoding/json"

// Tool resolvers read tools whose pinned shape was checked at the /v1 boundary:
// request tools, or a saved Agent's tools that resolveSavedTools stored. A
// decode failure is Core's own fault and is reported as a service error.
func resolveProgrammaticTool(raw json.RawMessage) (json.RawMessage, error) {
	input := struct {
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}{Enabled: true}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, &storedDataError{err}
	}
	return json.Marshal(input)
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

// Optional settings are resource data in every mode.
func (tool webSearchTool) resolveSettings() (json.RawMessage, error) {
	if tool.ContextSize == nil {
		value := "medium"
		tool.ContextSize = &value
	}
	return json.Marshal(tool)
}

// An omitted or null mode resolves to live, as the official service does. The
// Session keeps every pinned mode; the selected Harness's declaration decides
// whether it can run.
func resolveWebSearch(raw json.RawMessage) (json.RawMessage, error) {
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
