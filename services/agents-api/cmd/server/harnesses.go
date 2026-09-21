package main

import (
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
)

// Engine selection is independent of compute ownership. Operators may enable
// user-managed Runtime profiles without configuring a managed Provider.
func enabledHarnesses(defaultEngine string, managed *execution.RuntimeProviders) ([]string, error) {
	kinds := []string{defaultEngine}
	if managed != nil {
		for kind := range managed.EngineProviders {
			kinds = append(kinds, kind)
		}
	}
	if value := os.Getenv("AGENTS_API_HARNESSES"); value != "" {
		kinds = append(kinds, strings.Split(value, ",")...)
	}
	for i, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if _, known := (engine.Catalog{}).Lookup(kind); !known {
			return nil, errors.New("unknown configured harness")
		}
		kinds[i] = kind
	}
	slices.Sort(kinds)
	return slices.Compact(kinds), nil
}
