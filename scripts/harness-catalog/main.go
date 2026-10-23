// Command harness-catalog projects the built-in Harness declarations and the
// model-provider protocol vocabulary for tooling.
package main

import (
	"encoding/json"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

type harness struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

func declarations() []harness {
	var result []harness
	for _, kind := range builtin.Kinds() {
		declaration, _ := builtin.Registry().Lookup(kind)
		result = append(result, harness{Kind: kind, Label: declaration.Label})
	}
	return result
}

func main() {
	projection := struct {
		Harnesses []harness                `json:"harnesses"`
		Protocols []modelprovider.Protocol `json:"protocols"`
	}{declarations(), modelprovider.Protocols()}
	if err := json.NewEncoder(os.Stdout).Encode(projection); err != nil {
		panic(err)
	}
}
