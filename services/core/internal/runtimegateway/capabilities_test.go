package runtimegateway

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"testing"
)

func TestInvalidHeartbeatDiscardsPreviousDeclarationAndClosesTransport(t *testing.T) {
	session := newTestSession(newFakeConn(), "runtime", proto.Version, NewRegistry(), nil)
	valid, err := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}}})
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

func TestFirstHeartbeatAnnouncesAgentKinds(t *testing.T) {
	registry := NewRegistry()
	session := newTestSession(newFakeConn(), "runtime", proto.Version, registry, nil)
	heartbeat, err := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []bool{true, false} {
		session.handleHeartbeat(heartbeat)
		select {
		case <-registry.Announced():
			if !want {
				t.Fatal("repeated heartbeat announced again")
			}
		default:
			if want {
				t.Fatal("first agent kinds not announced")
			}
		}
	}
}
