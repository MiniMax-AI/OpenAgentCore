package claudesdk

import "github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

// nativeModelOptions is private SDK input, compiled only after the shared
// configuration declaration validates and owns the model parameters.
type nativeModelOptions struct {
	Effort   string         `json:"effort,omitempty"`
	Thinking map[string]any `json:"thinking,omitempty"`
}

func compileNativeModelOptions(config proto.HarnessConfig, effort string) *nativeModelOptions {
	if len(config) == 0 && effort == "" {
		return nil
	}
	options := &nativeModelOptions{Effort: effort}
	if value, exists := config["thinking"]; exists {
		options.Thinking = value.(map[string]any)
	}
	return options
}
