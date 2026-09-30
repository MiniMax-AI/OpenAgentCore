// Command harness-catalog projects adapter-owned provider declarations for tooling.
package main

import (
	"encoding/json"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

type provider struct {
	RequiresTokenLimits bool `json:"requires_token_limits"`
}

func declarations() map[string]map[string]provider {
	result := make(map[string]map[string]provider)
	for _, kind := range builtin.Kinds() {
		providers := make(map[string]provider)
		for _, declaration := range builtin.Configuration(kind).Providers {
			providers[declaration.Protocol] = provider{RequiresTokenLimits: declaration.RequiresTokenLimits}
		}
		result[kind] = providers
	}
	return result
}

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(declarations()); err != nil {
		panic(err)
	}
}
