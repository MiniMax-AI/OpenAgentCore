package codex

func validateNativeConfig(config map[string]any) bool {
	for key, value := range config {
		if key != "model_reasoning_effort" {
			return false
		}
		text, ok := value.(string)
		if !ok {
			return false
		}
		switch text {
		case "none", "minimal", "low", "medium", "high", "xhigh":
		default:
			return false
		}
	}
	return true
}
