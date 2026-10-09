package mcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

// environmentMCPFixture is an installed stdio MCP server.
func environmentMCPFixture() agent.EnvironmentMCP {
	return agent.EnvironmentMCP{InstallationRoot: "/private/runtime/capabilities", WorkspaceRoot: "/private/runtime/workspace", PackageRoot: "plugins/fixture", Server: agentplugin.MCPServer{
		Name: "proof.server", Type: "stdio", Command: "never-exec-before-sandbox", Args: []string{"private-argument"},
		EnvVars: []string{"USER_SELECTED"}, CWD: "resources",
	}}
}

// stdioBinding and httpBinding are MCP bindings as the agent host hands them
// to the view: a stdio server under its alias and an HTTP server at the
// Session gateway's endpoint.
func stdioBinding() agent.MCPBinding {
	return agent.MCPBinding{ServerLabel: "proof.server", ConnectionOrigin: "environment", CredentialAuthority: "none", Transport: "stdio",
		Stdio: &agent.EnvironmentMCP{Server: agentplugin.MCPServer{Name: "proof.server", Type: "stdio", Command: agent.ViewAlias(0)}}}
}

func httpBinding() agent.MCPBinding {
	return agent.MCPBinding{ServerLabel: "remote", ConnectionOrigin: "environment", CredentialAuthority: "none", Transport: "http", ServerURL: "http://127.0.0.1:4102/mcp/remote"}
}

// sessionParams returns the MCP servers and working directory that the ACP
// Session of the CLI recording into record received.
func sessionParams(t *testing.T, record string) (string, []map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(record + ".session")
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		SessionID string           `json:"sessionId"`
		Cwd       string           `json:"cwd"`
		MCP       []map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	return params.Cwd, params.MCP
}

func TestEnvironmentMCPRunsAliasInWorkspaceForNewAndLoadedSessions(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "load"}[resume], func(t *testing.T) {
			req := workspaceRequest(t)
			if resume {
				req.AgentSessionID = "native-1"
			}
			record := filepath.Join(t.TempDir(), "calls")
			if _, err := hostExecutor(t, t.Context(), helperInstall(t, "prepared", record), req, hostSession(t, stdioBinding())); err != nil {
				t.Fatal(err)
			}
			cwd, servers := sessionParams(t, record)
			process, err := os.ReadFile(record + ".cwd")
			workspace, pathErr := filepath.EvalSymlinks(req.WorkspaceRoot)
			if err != nil || pathErr != nil || string(process) != workspace || cwd != req.WorkspaceRoot {
				t.Fatalf("native process and ACP Session must use the declared workspace: process=%q ACP=%q", process, cwd)
			}
			if len(servers) != 2 || servers[0]["name"] != "oac_workspace" {
				t.Fatalf("environment MCP displaced workspace tools: %v", servers)
			}
			if server := servers[1]; server["name"] != "proof.server" || server["command"] != agent.ViewAlias(0) || !reflect.DeepEqual(server["args"], []any{}) || !reflect.DeepEqual(server["env"], []any{}) {
				t.Fatalf("stdio MCP = %v", server)
			}
			calls, _ := os.ReadFile(record)
			if strings.Contains(string(calls), "session/load") != resume {
				t.Fatalf("native attachment changed identity: %s", calls)
			}
		})
	}
}

func TestEnvironmentHTTPMCPRejectsCredentialsBeforePreparation(t *testing.T) {
	for _, name := range []string{"headers", "bearer"} {
		t.Run(name, func(t *testing.T) {
			binding := httpBinding()
			if name == "headers" {
				binding.HTTPHeaders = map[string]string{"X-Private": "secret"}
			} else {
				token := "confidential-http-token"
				binding.BearerToken = &token
			}
			if _, err := fakeInstall("node").prepare(workspaceRequest(t), hostSession(t, binding)); err == nil || strings.Contains(err.Error(), "confidential-http-token") {
				t.Fatal("credential accepted or exposed")
			}
		})
	}
}

func TestEnvironmentMCPCancelSettlesPendingObservationBeforeDone(t *testing.T) {
	resource, err := hostExecutor(t, t.Context(), helperInstall(t, "prepared-mcp-cancel", ""), workspaceRequest(t), hostSession(t, stdioBinding()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out := make(chan proto.Envelope, 16)
	session, err := resource.StartTurn(ctx, "run", proto.TextInput("invoke and wait"), out)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-out:
		var call proto.ToolCallPayload
		if event.Type != proto.TypeToolCall || json.Unmarshal(event.Payload, &call) != nil || call.Stage != "before" || call.Observation == nil || call.Observation.Server != "proof.server" {
			t.Fatal("real-time MCP start missing", event.Type)
		}
	case <-ctx.Done():
		t.Fatal("MCP start was not observed")
	}
	if err := session.Cancel(ctx); err != nil {
		t.Fatal("MCP cancellation failed", err)
	}
	settlement, err := session.(*Session).AwaitSettlement(ctx)
	if err != nil || !settlement.Reusable {
		t.Fatal("settled MCP scopes did not retain the owner", settlement, err)
	}
	closed, done := false, false
	for event := range out {
		if event.Type == proto.TypeToolCall {
			var call proto.ToolCallPayload
			_ = json.Unmarshal(event.Payload, &call)
			if call.ID != "native-call" || call.Stage != "after" || call.Observation.Status != "incomplete" {
				t.Fatal("cancel lost pending MCP identity or status")
			}
			closed = true
		}
		if event.Type == proto.TypeDone {
			if !closed {
				t.Fatal("Done preceded incomplete MCP observation")
			}
			done = true
		}
	}
	if !closed || !done {
		t.Fatal("cancellation did not settle observations")
	}
}

func writeMCPRegistry(path string, entries ...map[string]any) error {
	raw, err := json.Marshal(map[string]any{"version": 1, "servers": entries})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, "mcp-runtime-names.json"), raw, 0600)
}

func mcpRegistryEntry(server, segment, tool, toolSegment string) map[string]any {
	key, _ := json.Marshal([]string{"configured", server})
	return map[string]any{"key": string(key), "raw": server, "segment": segment, "tools": []map[string]string{{"raw": tool, "segment": toolSegment}}}
}

func TestEnvironmentHTTPMCPUsesEphemeralACPConfiguration(t *testing.T) {
	record := filepath.Join(t.TempDir(), "calls")
	e, err := hostExecutor(t, t.Context(), helperInstall(t, "prepared", record), workspaceRequest(t), hostSession(t, httpBinding()))
	if err != nil {
		t.Fatal(err)
	}
	_, servers := sessionParams(t, record)
	if len(servers) != 2 || servers[0]["name"] != "oac_workspace" {
		t.Fatalf("HTTP MCP displaced workspace tools: %v", servers)
	}
	if server := servers[1]; server["name"] != "remote" || server["type"] != "http" || server["url"] != httpBinding().ServerURL || !reflect.DeepEqual(server["headers"], []any{}) {
		t.Fatalf("invalid native HTTP projection: %v", server)
	}
	for _, name := range []string{"config.yaml", "mcp.json", "workspace-profile.json"} {
		body, err := os.ReadFile(filepath.Join(e.opts.DataDir, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if strings.Contains(string(body), httpBinding().ServerURL) {
			t.Fatalf("%s persists the Session's HTTP MCP", name)
		}
	}
}
