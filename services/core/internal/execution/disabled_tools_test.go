package execution

import (
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine/enginetest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestDisabledToolsUseCommonOperationQualification(t *testing.T) {
	raw := json.RawMessage(`{"agent":{"model":"model","tools":[{"type":"web_search","mode":"disabled"},{"type":"programmatic_tool_calling","enabled":false}]},"environment":{"type":"none"}}`)
	for _, kind := range []string{"codex", "claude_sdk", "mcode"} {
		if err := (Policy{}).ValidateSessionConfiguration(kind, raw); err != nil {
			t.Fatal(kind, err)
		}
	}
	for _, qualified := range []bool{false, true} {
		policy := Policy{Engines: engine.NewCatalog(map[string]engine.Profile{"new_harness": enginetest.Profile(func(p *engine.Profile) { p.ProgrammaticToolCallingDisable = proto.CapabilityFromBool(qualified) })})}
		if err := policy.ValidateSessionConfiguration("new_harness", raw); (err == nil) != qualified {
			t.Fatal("qualification differs", qualified, err)
		}
		if err := policy.ValidateSessionConfiguration("new_harness", json.RawMessage(`{"agent":{"model":"model"},"environment":{"type":"none"}}`)); err != nil {
			t.Fatal("omission acquired a new prerequisite", err)
		}
	}
}

func TestDisabledToolRequestPreservesIntentOnResume(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		snapshot := Snapshot{Agent: v1.Agent{Model: "model"}}
		if disabled {
			snapshot.Agent.Tools = []json.RawMessage{json.RawMessage(`{"type":"programmatic_tool_calling","enabled":false}`)}
		}
		before, _ := json.Marshal(snapshot)
		for _, nativeID := range []string{"", "native-session"} {
			request, err := (&Dispatcher{}).executionRequest(t.Context(), sessions.Session{ID: "session"}, snapshot, runtimedevice.KindCapabilities{}, sessions.ExecutionBinding{NativeSessionID: nativeID})
			if err != nil || request.ExecutionControls.DisableProgrammaticToolCalling != disabled || request.ExecutionControls.WebSearch != "disabled" || request.AgentSessionID != nativeID {
				t.Fatal(request, err)
			}
			if request.ValidateProgrammaticToolCallingDisable(true) != nil || (request.ValidateProgrammaticToolCallingDisable(false) != nil) != disabled {
				t.Fatal("runtime operation qualification differs")
			}
		}
		after, _ := json.Marshal(snapshot)
		if string(before) != string(after) {
			t.Fatal("snapshot mutated")
		}
	}
}

// TV-05: saved Agents store enabled and omitted-mode search, but such a snapshot
// fails admission qualification, device selection/preclaim tool decoding and
// dispatch request construction.
func TestEnabledWebSearchNeverReachesDispatch(t *testing.T) {
	for _, tool := range []string{
		`{"type":"web_search","mode":"live","context_size":"medium","allowed_domains":null,"location":null}`,
		`{"type":"web_search","mode":"cached","context_size":"medium","allowed_domains":null,"location":null}`,
		`{"type":"web_search","context_size":"medium","allowed_domains":null,"location":null}`,
		`{"type":"web_search","mode":null}`,
	} {
		tools := []json.RawMessage{json.RawMessage(tool)}
		if _, err := executionTools(tools); err == nil || err.Error() != "only disabled web search is qualified for execution" {
			t.Fatal(tool, err)
		}
		snapshot := Snapshot{Agent: v1.Agent{Model: "model", Tools: tools}}
		if _, err := (&Dispatcher{}).executionRequest(t.Context(), sessions.Session{ID: "session"}, snapshot, runtimedevice.KindCapabilities{}, sessions.ExecutionBinding{}); err == nil {
			t.Fatal("dispatch request built for", tool)
		}
		raw := json.RawMessage(`{"agent":{"model":"model","tools":[` + tool + `]},"environment":{"type":"none"}}`)
		for _, kind := range []string{"codex", "claude_sdk", "mcode"} {
			if err := (Policy{}).ValidateSessionConfiguration(kind, raw); err == nil {
				t.Fatal(kind, "admitted", tool)
			}
		}
	}
	if _, err := executionTools([]json.RawMessage{json.RawMessage(`{"type":"web_search","mode":"disabled","context_size":"medium","allowed_domains":null,"location":null}`)}); err != nil {
		t.Fatal(err)
	}
}
