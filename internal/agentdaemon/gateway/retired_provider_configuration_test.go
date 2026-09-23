package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestRetiredProviderConfigurationDoesNotAffectHeartbeatAdmission(t *testing.T) {
	const oldHeartbeat = `{"supported_agent_kinds":[{"kind":"codex","available":true,"capabilities":{"function_tools":true},"provider_configuration":{"schema_version":1,"providers":[{"protocol":"responses"}]}}]}`
	var heartbeat proto.HeartbeatPayload
	if err := json.Unmarshal([]byte(oldHeartbeat), &heartbeat); err != nil {
		t.Fatal(err)
	}
	s := &Session{}
	s.setSupportedAgentKinds(deviceKindsFromHeartbeat(heartbeat))
	info, found, known := s.AgentKindStatus("codex")
	if !found || !known || !info.Available || !info.Capabilities.FunctionTools {
		t.Fatal("retired metadata changed native availability or execution capabilities")
	}
	wire, err := json.Marshal(heartbeat)
	if err != nil || strings.Contains(string(wire), "provider_configuration") {
		t.Fatal("retired provider metadata is still advertised", err)
	}
}
