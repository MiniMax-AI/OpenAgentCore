package mcode

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func TestNativeConfigDoesNotPretendToSupportParameters(t *testing.T) {
	req := testRequest(t)
	req.HarnessConfig = proto.HarnessConfig(`{}`)
	if _, err := prepareOptions(req); err != nil {
		t.Fatal(err)
	}
	req.HarnessConfig = proto.HarnessConfig(`{"temperature":0.5}`)
	if _, err := prepareOptions(req); err != harnessconfig.ErrHarnessConfig {
		t.Fatalf("unsupported parameter accepted: %v", err)
	}
}
