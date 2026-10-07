package runtimegateway

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

func TestInvalidHeartbeatDiscardsPreviousDeclarationAndClosesTransport(t *testing.T) {
	heartbeat := func(kind proto.SupportedAgentKind) proto.Envelope {
		env, err := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{kind}})
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	for name, invalid := range map[string]proto.Envelope{
		"undeclared capabilities": {Type: proto.TypeHeartbeat, Payload: []byte(`{"supported_agent_kinds":[{"kind":"mcode","available":true}]}`)},
		// MiniMax Code's static declaration has no function tools.
		"widening":        heartbeat(proto.SupportedAgentKind{Kind: "mcode", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{FunctionTools: proto.CapabilitySupported})}),
		"unknown Harness": heartbeat(proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}),
	} {
		t.Run(name, func(t *testing.T) {
			session := NewSession(newFakeConn(), "runtime", "workspace", proto.Version, NewRegistry(), nil)
			session.handleHeartbeat(heartbeat(proto.SupportedAgentKind{Kind: "mcode", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{MCPHTTPTools: proto.CapabilitySupported})}))
			if _, found, _ := session.AgentKindStatus("mcode"); !found {
				t.Fatal("narrowing declaration not admitted")
			}
			session.handleHeartbeat(invalid)
			if !session.IsClosed() {
				t.Fatal("invalid peer remains usable")
			}
			if _, found, known := session.AgentKindStatus("mcode"); found || !known {
				t.Fatal("old declaration survived invalid heartbeat")
			}
		})
	}
}
