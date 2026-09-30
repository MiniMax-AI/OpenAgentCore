package api

import (
	"encoding/json"
	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func tokenUsage(raw json.RawMessage) *v1.TokenUsage {
	var usage *v1.TokenUsage
	if json.Unmarshal(raw, &usage) != nil {
		return nil
	}
	return usage
}
