package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestDiscoveryResourceProjectionRetainsExecutionConfiguration(t *testing.T) {
	raw := json.RawMessage(`{"agent":{"id":"agent","model":"model","tools":[{"type":"tool_search"},{"type":"function","name":"lookup","description":"Lookup","parameters":{"type":"object"},"defer_loading":true}]},"environment":{"type":"none"}}`)
	session := store.Session{ID: "session", Configuration: raw}
	resource, err := sessionResponse(session, "")
	if err != nil || len(resource.Agent.Tools) != 1 || !strings.Contains(string(resource.Agent.Tools[0]), `"defer_loading":true`) {
		t.Fatal(resource.Agent.Tools, err)
	}
	if !strings.Contains(string(session.Configuration), `"type":"tool_search"`) {
		t.Fatal("public projection changed frozen execution configuration")
	}
}
