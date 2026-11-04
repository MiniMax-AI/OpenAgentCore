// Command harness-catalog projects the built-in Harness declarations and the
// model-provider protocol vocabulary for tooling.
package main

import (
	"encoding/json"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

type harness struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Component and Version name the native installer component and its pin.
	Component string `json:"component"`
	Version   string `json:"version"`
	// Protocols are the model-provider protocols, in declaration order.
	Protocols []string  `json:"protocols"`
	Execution execution `json:"execution"`
	// MessageImages and MCPHTTPRequired report the declared capabilities.
	MessageImages   bool `json:"message_images"`
	MCPHTTPRequired bool `json:"mcp_http_required"`
}

// execution lists the execution option values the Harness admits.
type execution struct {
	WebSearchModes     []string `json:"web_search_modes"`
	ReasoningEfforts   []string `json:"reasoning_efforts"`
	ReasoningSummaries []string `json:"reasoning_summaries"`
	ServiceTiers       []string `json:"service_tiers"`
	TextVerbosities    []string `json:"text_verbosities"`
}

func values(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func declarations() []harness {
	var result []harness
	for _, kind := range builtin.Kinds() {
		declaration, _ := builtin.Registry().Lookup(kind)
		protocols := []string{}
		for _, provider := range declaration.Configuration.Providers {
			protocols = append(protocols, string(provider.Protocol))
		}
		options := declaration.Execution
		result = append(result, harness{
			Kind: kind, Label: declaration.Label, Component: declaration.Native.Component, Version: declaration.Native.Version,
			Protocols: protocols,
			Execution: execution{
				WebSearchModes: values(options.WebSearchModes), ReasoningEfforts: values(options.ReasoningEfforts),
				ReasoningSummaries: values(options.ReasoningSummaries), ServiceTiers: values(options.ServiceTiers),
				TextVerbosities: values(options.TextVerbosities),
			},
			MessageImages:   declaration.Capabilities.MessageImages == proto.CapabilitySupported,
			MCPHTTPRequired: declaration.Capabilities.MCPHTTPRequired == proto.CapabilitySupported,
		})
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
