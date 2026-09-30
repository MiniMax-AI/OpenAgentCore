package claudesdk

import "github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

// nativeModelOptions is private SDK input, compiled only after the shared
// configuration declaration validates and owns the model parameters.
type nativeModelOptions struct {
	Effort   string         `json:"effort,omitempty"`
	Thinking map[string]any `json:"thinking,omitempty"`
}

func compileNativeModelOptions(config proto.HarnessConfig) *nativeModelOptions {
	if len(config) == 0 {
		return nil
	}
	options := &nativeModelOptions{}
	if value, exists := config["effort"]; exists {
		options.Effort = value.(string)
	}
	if value, exists := config["thinking"]; exists {
		options.Thinking = value.(map[string]any)
	}
	return options
}
