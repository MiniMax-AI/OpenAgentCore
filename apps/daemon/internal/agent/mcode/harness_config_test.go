package mcode

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"testing"
)

func TestNativeConfigDoesNotPretendToSupportParameters(t *testing.T) {
	req := testRequest(t)
	req.AgentOptions["harness_config"] = map[string]any{}
	if _, err := prepareOptions(testStateRoot(t), req); err != nil {
		t.Fatal(err)
	}
	req.AgentOptions["harness_config"] = map[string]any{"temperature": 0.5}
	if _, err := prepareOptions(testStateRoot(t), req); err != harnessconfig.ErrHarnessConfig {
		t.Fatalf("unsupported parameter accepted: %v", err)
	}
}
