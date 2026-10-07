package runtimegateway

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"testing"
)

func TestInvalidHeartbeatDiscardsPreviousDeclarationAndClosesTransport(t *testing.T) {
	session := NewSession(newFakeConn(), "runtime", "workspace", proto.Version, NewRegistry(), nil)
	valid, err := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}}})
	if err != nil {
		t.Fatal(err)
	}
	session.handleHeartbeat(valid)
	if _, found, _ := session.AgentKindStatus("fixture"); !found {
		t.Fatal("valid declaration not admitted")
	}
	session.handleHeartbeat(proto.Envelope{Type: proto.TypeHeartbeat, Payload: []byte(`{"supported_agent_kinds":[{"kind":"fixture","available":true}]}`)})
	if !session.IsClosed() {
		t.Fatal("invalid peer remains usable")
	}
	if _, found, known := session.AgentKindStatus("fixture"); found || !known {
		t.Fatal("old declaration survived invalid heartbeat")
	}
}
