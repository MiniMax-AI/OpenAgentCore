package pi

import "github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

// validateNativeConfig accepts only the reasoning levels Pi models expose.
// Pi maps each level to its native thinking budget; unknown keys, other types
// and values Pi would silently ignore are rejected before native resources start.
func validateNativeConfig(config proto.HarnessConfig) bool {
	for key, value := range config {
		if key != "thinking_level" {
			return false
		}
		level, ok := value.(string)
		if !ok {
			return false
		}
		switch level {
		case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		default:
			return false
		}
	}
	return true
}
