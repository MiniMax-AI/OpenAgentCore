package claudesdk

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"math"
)

func validateNativeConfig(config proto.HarnessConfig) bool {
	for key, value := range config {
		switch key {
		case "effort":
			text, ok := value.(string)
			if !ok {
				return false
			}
			switch text {
			case "low", "medium", "high", "xhigh", "max":
			default:
				return false
			}
		case "thinking":
			thinking, ok := value.(map[string]any)
			if !ok || !validThinking(thinking) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// The pinned SDK's native ThinkingConfig union; deprecated maxThinkingTokens
// is intentionally absent so a thinking budget has exactly one owner.
func validThinking(thinking map[string]any) bool {
	kind, ok := thinking["type"].(string)
	if !ok || (kind != "adaptive" && kind != "enabled" && kind != "disabled") {
		return false
	}
	for key, value := range thinking {
		switch key {
		case "type":
		case "budgetTokens":
			n, ok := value.(float64)
			if kind != "enabled" || !ok || n < 1 || n > 9007199254740991 || math.Trunc(n) != n {
				return false
			}
		case "display":
			text, ok := value.(string)
			if kind == "disabled" || !ok || (text != "summarized" && text != "omitted") {
				return false
			}
		default:
			return false
		}
	}
	return true
}
