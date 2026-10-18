package pi

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestValidateNativeConfig(t *testing.T) {
	valid := []proto.HarnessConfig{
		{},
		{"thinking_level": "off"},
		{"thinking_level": "medium"},
		{"thinking_level": "max"},
	}
	for _, config := range valid {
		if !validateNativeConfig(config) {
			t.Fatalf("expected %v to be accepted", config)
		}
	}
	invalid := []proto.HarnessConfig{
		{"thinking_level": "none"},
		{"thinking_level": "highest"},
		{"thinking_level": 3},
		{"thinking_level": nil},
		{"thinking": "high"},
		{"thinking_level": "high", "model": "other"},
	}
	for _, config := range invalid {
		if validateNativeConfig(config) {
			t.Fatalf("expected %v to be rejected", config)
		}
	}
}
