package runtimegateway

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"reflect"
	"testing"
)

func TestCapabilityProjectionCoversEveryField(t *testing.T) {
	declaration := prototest.Capabilities(proto.AgentKindCapabilities{})
	wireType, deviceType := reflect.TypeOf(declaration), reflect.TypeOf(runtimedevice.KindCapabilities{})
	if wireType.NumField() != deviceType.NumField() {
		t.Fatal("wire and device capability inventories differ")
	}
	for i := 0; i < wireType.NumField(); i++ {
		field := wireType.Field(i)
		t.Run(field.Name, func(t *testing.T) {
			caps := declaration
			reflect.ValueOf(&caps).Elem().Field(i).Set(reflect.ValueOf(proto.CapabilitySupported))
			kinds := deviceKindsFromHeartbeat(proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "fixture", Available: true, Capabilities: caps}}})
			actual := reflect.ValueOf(kinds[0].Capabilities)
			for j := 0; j < deviceType.NumField(); j++ {
				if actual.Field(j).Bool() != (deviceType.Field(j).Name == field.Name) {
					t.Fatal("projection lost or crossed a capability field")
				}
			}
		})
	}
}

func TestInvalidHeartbeatDiscardsPreviousDeclarationAndClosesTransport(t *testing.T) {
	session := NewSession(newFakeConn(), "runtime", "workspace", proto.Version, NewRegistry(), nil)
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
