package claudesdk

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestSubagentConfigurationUsesExplicitRequestAndFrozenLimit(t *testing.T) {
	config := workspaceFixture(t)
	req := workspaceRequest()
	start, _, err := prepare(config, req)
	if err != nil || start.Subagents != nil {
		t.Fatal("ordinary execution changed", err)
	}
	req.DisableSubagents, req.ObserveSubagentIdentities = false, true
	start, _, err = prepare(config, req)
	if err != nil || start.Subagents == nil || start.Subagents.MaxConcurrent != 6 {
		t.Fatal("missing default native admission limit", err)
	}
	limit := 2
	req.MaxConcurrentSubagents = &limit
	start, _, err = prepare(config, req)
	limit = 4
	if err != nil || start.Subagents.MaxConcurrent != 2 {
		t.Fatal("subagent configuration was not frozen", err)
	}
}

func TestSubagentConfigurationRejectsUnqualifiedAuthority(t *testing.T) {
	config := workspaceFixture(t)
	for _, change := range []func(*proto.PromptRequestPayload){
		func(r *proto.PromptRequestPayload) { r.DisableSubagents = true },
		func(r *proto.PromptRequestPayload) { n := 0; r.MaxConcurrentSubagents = &n },
		func(r *proto.PromptRequestPayload) { r.FunctionTools = []proto.FunctionTool{{Name: "function"}} },
		func(r *proto.PromptRequestPayload) { v := []proto.MCPHTTPServer{}; r.MCPHTTPServers = &v },
	} {
		req := workspaceRequest()
		req.DisableSubagents, req.ObserveSubagentIdentities = false, true
		change(&req)
		if _, _, err := prepare(config, req); err == nil {
			t.Fatal("unqualified subagent combination accepted")
		}
	}
	if (RuntimeInfo{}).SupportsSubagents() || !(RuntimeInfo{Features: []string{"subagent_resources"}}).SupportsSubagents() {
		t.Fatal("subagent readiness did not require the packaged feature")
	}
}
