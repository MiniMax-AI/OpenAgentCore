// Package mcode owns the qualified MiniMax Code provider declaration.
package mcode

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

func Configuration() harnessconfig.Configuration {
	return harnessconfig.Configuration{Providers: []harnessconfig.Provider{
		{Protocol: "anthropic", RequiresTokenLimits: true},
		{Protocol: "responses", RequiresTokenLimits: true},
		{Protocol: "chat_completions", RequiresTokenLimits: true},
	}}
}
