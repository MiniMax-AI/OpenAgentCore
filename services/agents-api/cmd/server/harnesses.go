package main

import (
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
)

// Engine selection is independent of compute ownership. Operators may enable
// user-managed Runtime profiles without configuring a managed Provider.
func enabledHarnesses(defaultEngine string) ([]string, error) {
	kinds := []string{defaultEngine}
	if value := os.Getenv("OAC_HARNESSES"); value != "" {
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
