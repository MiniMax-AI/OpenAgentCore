// Package pi owns the qualified Pi model provider configuration declaration.
package pi

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

// Configuration declares the model provider protocols Pi can connect to
// natively. Pi speaks the OpenAI Chat Completions, OpenAI Responses and
// Anthropic Messages APIs directly; Core never converts between them.
func Configuration() harnessconfig.Configuration {
	return harnessconfig.Configuration{
		Providers: []harnessconfig.Provider{
			{Protocol: "chat_completions"},
			{Protocol: "responses"},
			{Protocol: "anthropic"},
		},
		ValidateNativeConfig: validateNativeConfig,
	}
}
