package proto_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
)

func TestCapabilityDeclarationIsCompleteOnBothSides(t *testing.T) {
	caps := prototest.Capabilities(proto.AgentKindCapabilities{})
	raw, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf(caps)
	if len(fields) != typ.NumField() {
		t.Fatal("wire projection lost capability fields")
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := field.Tag.Get("json")
		t.Run(key, func(t *testing.T) {
			if string(fields[key]) != "false" {
				t.Fatalf("explicit unsupported omitted: %s", raw)
			}
			for _, value := range []proto.CapabilitySupport{proto.CapabilityUnspecified, proto.CapabilitySupport(255)} {
				invalid := caps
				reflect.ValueOf(&invalid).Elem().Field(i).Set(reflect.ValueOf(value))
				if invalid.ValidateDeclaration() == nil {
					t.Fatal("undeclared/invalid capability accepted")
				}
				if _, err := json.Marshal(invalid); err == nil {
					t.Fatal("invalid declaration encoded")
				}
			}
			original := fields[key]
			for _, replacement := range []json.RawMessage{nil, []byte("null"), []byte("0"), []byte(`"true"`)} {
				if replacement == nil {
					delete(fields, key)
				} else {
					fields[key] = replacement
				}
				wire, _ := json.Marshal(fields)
				decoded := caps // Decode must not preserve old values for omitted fields.
				if json.Unmarshal(wire, &decoded) == nil {
					t.Fatal("incomplete declaration decoded")
				}
			}
			fields[key] = []byte("true")
			wire, _ := json.Marshal(fields)
			var decoded proto.AgentKindCapabilities
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			if reflect.ValueOf(decoded).Field(i).Interface() != proto.CapabilitySupported {
				t.Fatal("supported field lost")
			}
			fields[key] = original
		})
	}
	fields["future_private_value"] = []byte("false")
	wire, _ := json.Marshal(fields)
	if json.Unmarshal(wire, &caps) == nil {
		t.Fatal("unknown declaration accepted")
	}
}

func TestHeartbeatRejectsMissingCapabilityObject(t *testing.T) {
	for _, raw := range []string{
		`{"supported_agent_kinds":[{"kind":"codex","available":true}]}`,
		`{"supported_agent_kinds":[{"kind":"codex","available":false,"capabilities":null}]}`,
	} {
		var heartbeat proto.HeartbeatPayload
		if json.Unmarshal([]byte(raw), &heartbeat) == nil {
			t.Fatal("missing declaration accepted")
		}
	}
}
