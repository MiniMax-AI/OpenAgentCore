package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Operator options use the existing transient adapter configuration path. They
// are never public Session configuration. Hosted creation freezes its model
// provider bundle separately in the encrypted Session credential snapshot.
func executionOptions() (func(context.Context, store.Session) (map[string]any, error), error) {
	resolve, _, err := executionOptionsConfiguration()
	return resolve, err
}

func executionOptionsConfiguration() (func(context.Context, store.Session) (map[string]any, error), map[string]bool, error) {
	file := os.Getenv("AGENTS_API_EXECUTION_OPTIONS_FILE")
	if file == "" {
		return nil, map[string]bool{}, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, errors.New("cannot read AGENTS_API_EXECUTION_OPTIONS_FILE")
	}
	var check map[string]any
	if json.Unmarshal(raw, &check) != nil || check == nil {
		return nil, nil, errors.New("execution options must contain a JSON object")
	}
	defaultEngine := os.Getenv("AGENTS_API_ENGINE")
	if defaultEngine == "" {
		defaultEngine = "codex"
	}
	var byHarness map[string]json.RawMessage
	if value, exists := check["by_harness"]; exists {
		encoded, _ := json.Marshal(value)
		if len(check) != 1 || json.Unmarshal(encoded, &byHarness) != nil || byHarness == nil {
			return nil, nil, errors.New("execution by_harness options must be an exclusive object")
		}
		for _, entry := range byHarness {
			var object map[string]any
			if json.Unmarshal(entry, &object) != nil || object == nil {
				return nil, nil, errors.New("execution harness options must be objects")
			}
		}
	}
	configured := make(map[string]bool)
	if byHarness != nil {
		for harness, entry := range byHarness {
			var object map[string]any
			_ = json.Unmarshal(entry, &object)
			configured[harness] = modelProviderEndpointConfigured(harness, object)
		}
	} else {
		configured[defaultEngine] = modelProviderEndpointConfigured(defaultEngine, check)
	}
	resolve := func(_ context.Context, session store.Session) (map[string]any, error) {
		selected := raw
		if byHarness != nil {
			var ok bool
			selected, ok = byHarness[session.Engine]
			if !ok {
				return nil, errors.New("execution options are unavailable for the selected harness")
			}
		} else if session.Engine != "" && session.Engine != defaultEngine {
			return nil, errors.New("configure separate execution options for the selected harness")
		}
		// Request assembly may extend the map; no Session may mutate another's options.
		var options map[string]any
		err := json.Unmarshal(selected, &options)
		return options, err
	}
	return resolve, configured, nil
}

func modelProviderEndpointConfigured(harness string, options map[string]any) bool {
	key := map[string]string{"codex": "codex_provider", "claude_sdk": "claude_provider", "mcode": "mcode_provider"}[harness]
	provider, ok := options[key].(map[string]any)
	if !ok {
		return false
	}
	if harness == "mcode" {
		nativeOptions, ok := provider["options"].(map[string]any)
		if !ok {
			return false
		}
		baseURL, ok := nativeOptions["baseURL"].(string)
		return ok && strings.TrimSpace(baseURL) != ""
	}
	baseURL, ok := provider["base_url"].(string)
	return ok && strings.TrimSpace(baseURL) != ""
}
