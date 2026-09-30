package gateway

import (
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
	"testing"
)

func TestFunctionCapabilitySurvivesHeartbeatMapping(t *testing.T) {
	for _, supported := range []bool{false, true} {
		kinds := deviceKindsFromHeartbeat(proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{FunctionTools: proto.CapabilityFromBool(supported), FunctionResultImages: proto.CapabilityFromBool(supported)})}}})
		s := &Session{}
		s.setSupportedAgentKinds(kinds)
		info, found, known := s.AgentKindStatus("codex")
		if !found || !known || info.Capabilities.FunctionTools != supported || info.Capabilities.FunctionResultImages != supported {
			t.Fatal(info, found, known)
		}
	}
}
