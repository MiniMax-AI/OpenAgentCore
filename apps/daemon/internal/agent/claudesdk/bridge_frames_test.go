package claudesdk

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// The TypeScript bridge reads the same file in packages/claude-sdk-adapter/tests/bridge_frames.test.mjs.
func TestBridgeFramesMatchSharedFixtures(t *testing.T) {
	data, err := os.ReadFile("../../../../../packages/claude-sdk-adapter/fixtures/bridge-frames.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		ToBridge   []json.RawMessage `json:"to_bridge"`
		FromBridge []json.RawMessage `json:"from_bridge"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	strict := func(raw json.RawMessage, into any) error {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		return decoder.Decode(into)
	}
	for _, raw := range fixtures.ToBridge {
		var kind struct{ Type string }
		if err := json.Unmarshal(raw, &kind); err != nil {
			t.Fatal(err)
		}
		var frame any
		switch kind.Type {
		case "executor_prepare":
			frame = &startRequest{}
		case "turn_start":
			frame = &turnStartFrame{}
		case "turn_cancel":
			frame = &turnCancelFrame{}
		case "steer":
			frame = &steerFrame{}
		case "function_result":
			frame = &functionResultFrame{}
		default:
			t.Fatalf("fixture frame %q has no Go producer", kind.Type)
		}
		if err := strict(raw, frame); err != nil {
			t.Fatalf("%s: %v", kind.Type, err)
		}
		// Encoding must reproduce every fixture field, so no field is dropped or renamed.
		encoded, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		if json.Unmarshal(raw, &want) != nil || json.Unmarshal(encoded, &got) != nil {
			t.Fatal("invalid JSON")
		}
		if !bytes.Equal(canonical(t, want), canonical(t, got)) {
			t.Errorf("%s: Go encodes\n%s\nfixture\n%s", kind.Type, encoded, raw)
		}
	}
	for _, raw := range fixtures.FromBridge {
		var event bridgeEvent
		if err := strict(raw, &event); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func canonical(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
