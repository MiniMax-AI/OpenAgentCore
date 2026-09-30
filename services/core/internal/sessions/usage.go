package sessions

import (
	"encoding/json"
	"math"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// MeasuredUsage interprets the token measurement an execution observation of
// kind carries: a usage report, a done payload, a terminal execution outcome or
// an applied cancellation receipt. It returns nil unless the breakdown is
// complete and consistent: every counter present and non-negative, cached
// tokens within input, reasoning tokens within output and the total the sum of
// input and output. An explicit zero is a measurement.
func MeasuredUsage(kind string, raw json.RawMessage) *v1.TokenUsage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	if kind == "cancel_receipt" {
		var applied bool
		if json.Unmarshal(object["applied"], &applied) != nil || !applied {
			return nil
		}
		raw = object["outcome"]
		object = nil
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
		kind = "done"
	}
	if strings.HasPrefix(kind, "execution_") {
		raw = object["done"]
		object = nil
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
		kind = "done"
	}
	if kind == "done" {
		raw = object["usage"]
		object = nil
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
	} else if kind != "usage" {
		return nil
	}

	var tokens map[string]*int64
	if json.Unmarshal(object["tokens"], &tokens) != nil {
		return nil
	}
	for _, key := range []string{"input_tokens", "output_tokens", "cached_input_tokens", "reasoning_output_tokens", "total_tokens"} {
		if tokens[key] == nil || *tokens[key] < 0 {
			return nil
		}
	}
	input, output, cached, reasoning, total := *tokens["input_tokens"], *tokens["output_tokens"], *tokens["cached_input_tokens"], *tokens["reasoning_output_tokens"], *tokens["total_tokens"]
	if cached > input || reasoning > output || input > math.MaxInt64-output || total != input+output {
		return nil
	}
	return &v1.TokenUsage{InputTokens: input, OutputTokens: output, TotalTokens: total, InputTokensDetails: v1.InputTokenDetails{CachedTokens: cached}, OutputTokensDetails: v1.OutputTokenDetails{ReasoningTokens: reasoning}}
}
