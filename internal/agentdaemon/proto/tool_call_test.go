package proto

import (
	"encoding/json"
	"testing"
)

func TestToolCallRejectsEngineSnapshot(t *testing.T) {
	var call ToolCallPayload
	if json.Unmarshal([]byte(`{"id":"tool","stage":"after","native_item":{"type":"commandExecution"}}`), &call) == nil {
		t.Fatal("accepted engine-specific snapshot in common wire")
	}
}
