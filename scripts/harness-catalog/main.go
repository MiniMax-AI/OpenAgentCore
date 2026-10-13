// Command harness-catalog projects adapter-owned provider declarations and the
// model-provider protocol vocabulary for tooling.
package main

import (
	"encoding/json"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
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
	projection := struct {
		Harnesses map[string]map[string]provider `json:"harnesses"`
		Protocols []modelprovider.Protocol       `json:"protocols"`
	}{declarations(), modelprovider.Protocols()}
	if err := json.NewEncoder(os.Stdout).Encode(projection); err != nil {
		panic(err)
	}
}
