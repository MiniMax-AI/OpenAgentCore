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

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func environmentMCPFixture() proto.EnvironmentMCP {
	return proto.EnvironmentMCP{InstallationRoot: "/private/runtime/capabilities", PackageRoot: "plugins/fixture", Server: proto.PluginMCPServer{
		Name: "proof.server", Type: "stdio", Command: "never-exec-before-sandbox", Args: []string{"private-argument"},
		EnvVars: []string{"USER_SELECTED"}, CWD: "resources",
	}}
}

func TestEnvironmentMCPUsesFixedLauncherForNewAndLoadedSessions(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "load"}[resume], func(t *testing.T) {
			c, req, record := workspaceFixture(t)
			req.LocalEnvironment.MCP = []proto.EnvironmentMCP{environmentMCPFixture()}
			if resume {
				req.AgentSessionID = "native-1"
			}
			t.Setenv("USER_SELECTED", "must-not-resolve-from-daemon")
			t.Setenv("MODEL_SECRET", "must-not-forward")
			resource, err := NewExecutorFactory(testStateRoot(t), &c)(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = resource.Close(context.Background()) })
			raw, err := os.ReadFile(record + ".session")
			if err != nil {
				t.Fatal(err)
			}
			var params struct {
				SessionID string `json:"sessionId"`
				Cwd       string `json:"cwd"`
				MCP       []struct {
					Name, Command string
					Args          []string
					Env           []map[string]string
				} `json:"mcpServers"`
			}
			if json.Unmarshal(raw, &params) != nil || len(params.MCP) != 1 {
				t.Fatal("native Session did not receive exactly the declared MCP server")
			}
			cwd, err := os.ReadFile(record + ".cwd")
			workspace, pathErr := filepath.EvalSymlinks(req.LocalEnvironment.WorkspaceRoot)
			if err != nil || pathErr != nil || string(cwd) != workspace || params.Cwd != req.LocalEnvironment.WorkspaceRoot {
				t.Fatalf("native process and ACP Session must use the declared workspace: process=%q ACP=%q", cwd, params.Cwd)
			}
			server := params.MCP[0]
			executable, _ := os.Executable()
			if server.Name != "proof.server" || server.Command != executable || server.Env == nil || len(server.Env) != 0 ||
				!reflect.DeepEqual(server.Args, []string{"runtime-mcp-exec", "/private/runtime/capabilities", "plugins/fixture", "proof.server"}) {
				t.Fatal("ACP declaration bypassed the shared Runtime launcher")
			}
			if (params.SessionID != "") != resume || strings.Contains(string(raw), "must-not") || strings.Contains(string(raw), "private-argument") {
				t.Fatal("native attachment changed identity or exposed private inputs")
			}
		})
	}
}

func TestEnvironmentMCPRejectsUnqualifiedAuthorityBeforePreparation(t *testing.T) {
	for _, name := range []string{"http-bearer-insecure", "http-bearer-missing", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			c, req, _ := workspaceFixture(t)
			req.LocalEnvironment.MCP = []proto.EnvironmentMCP{environmentMCPFixture()}
			switch name {
			case "http-bearer-insecure", "http-bearer-missing":
				req.LocalEnvironment.MCP[0].Server = proto.PluginMCPServer{Name: "remote", Type: "http", URL: "https://example.invalid/mcp"}
				if name == "http-bearer-missing" {
					req.LocalEnvironment.MCP[0].Server.BearerTokenEnvVar = "SELECTED_TOKEN"
				}
				if name == "http-bearer-insecure" {
					req.LocalEnvironment.MCP[0].Server.URL = "http://example.invalid/mcp"
					token := "confidential-http-token"
					req.LocalEnvironment.MCP[0].BearerToken = &token
				}
			case "duplicate":
				req.LocalEnvironment.MCP = append(req.LocalEnvironment.MCP, environmentMCPFixture())
			}
			root := t.TempDir()
			_, err := NewExecutorFactory(root, &c)(context.Background(), req)
			if err == nil || strings.Contains(err.Error(), "confidential-http-token") {
				t.Fatal("unqualified declaration accepted or credential exposed")
			}
			if entries, _ := os.ReadDir(root); len(entries) != 0 {
				t.Fatalf("native files written before MCP was rejected: %v", entries)
			}
		})
	}
}

func TestEnvironmentMCPCancelSettlesPendingObservationBeforeDone(t *testing.T) {
	c, req, _ := workspaceFixture(t)
	req.LocalEnvironment.MCP = []proto.EnvironmentMCP{environmentMCPFixture()}
	script, err := os.ReadFile(c.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Binary, []byte(strings.Replace(string(script), "HELPER=prepared", "HELPER=prepared-mcp-cancel", 1)), 0700); err != nil {
		t.Fatal(err)
	}
	resource, err := NewExecutorFactory(testStateRoot(t), &c)(t.Context(), req)
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
	t.Cleanup(func() { _ = resource.Close(context.Background()) })
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
		t.Fatal("stopped nonreusable MCP owner reported cancellation failure", err)
	}
	settlement, err := session.(*Session).AwaitSettlement(ctx)
	if err != nil || settlement.Reusable {
		t.Fatal("pending MCP work retained a reusable owner", settlement, err)
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
	for _, authenticated := range []bool{false, true} {
		c, req, record := workspaceFixture(t)
		item := proto.EnvironmentMCP{Server: proto.PluginMCPServer{Name: "remote", Type: "http", URL: "https://example.invalid/mcp"}}
		const token = "private-mcp-canary"
		if authenticated {
			value := token
			item.BearerToken = &value
		}
		req.LocalEnvironment.MCP = []proto.EnvironmentMCP{item}
		resource, err := NewExecutorFactory(testStateRoot(t), &c)(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resource.Close(context.Background()) })
		raw, err := os.ReadFile(record + ".session")
		if err != nil {
			t.Fatal(err)
		}
		var params struct {
			MCP []struct {
				Name, Type, URL string
				Headers         []map[string]string
			} `json:"mcpServers"`
		}
		if json.Unmarshal(raw, &params) != nil || len(params.MCP) != 1 {
			t.Fatal("native Session did not receive exactly the declared MCP server")
		}
		server := params.MCP[0]
		if server.Name != "remote" || server.Type != "http" || server.URL != item.Server.URL || server.Headers == nil {
			t.Fatal("invalid native HTTP projection")
		}
		if authenticated {
			if !reflect.DeepEqual(server.Headers, []map[string]string{{"name": "Authorization", "value": "Bearer " + token}}) {
				t.Fatal("credential missing from ACP transport")
			}
		} else if len(server.Headers) != 0 {
			t.Fatal("anonymous MCP inherited credentials")
		}
		dataDir := resource.(*executor).opts.DataDir
		body, err := os.ReadFile(filepath.Join(dataDir, "config.yaml"))
		if err != nil || strings.Contains(string(body), token) {
			t.Fatal("MCP credential persisted in native configuration", err)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "mcp.json")); !os.IsNotExist(err) {
			t.Fatal("adapter wrote a native MCP file")
		}
	}
}

func TestPublicEnvironmentHTTPMCPKeepsCredentialTransient(t *testing.T) {
	c, req, _ := workspaceFixture(t)
	token := "selected-public-vault-canary"
	req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "environment", ServerLabel: "remote", ServerURL: "https://example.test/mcp", BearerToken: &token}}
	opts, err := prepareWorkspaceOptions(testStateRoot(t), c, req)
	if err != nil {
		t.Fatal(err)
	}
	servers, err := runtimeMCP(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(servers)
	if err != nil || !strings.Contains(string(raw), "Bearer "+token) {
		t.Fatal("selected token not supplied to native ACP")
	}
	err = filepath.WalkDir(opts.DataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(value), token) {
			t.Fatal("public credential persisted in native state")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMCPHeadersReachACPHeaders(t *testing.T) {
	req := testRequest(t)
	req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "fixture", ServerURL: "https://mcp.example/mcp", Headers: map[string]string{"X-Tenant": "a"}}}
	servers, err := runtimeMCP(req)
	want := []map[string]string{{"name": "X-Tenant", "value": "a"}}
	if err != nil || len(servers) != 1 || !reflect.DeepEqual(servers[0]["headers"], want) {
		t.Fatalf("MCP = %v, %v", servers, err)
	}
}

func TestServiceOriginMCPOnNonePlacement(t *testing.T) {
	req := testRequest(t)
	req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "fixture", ServerURL: "https://mcp.example/mcp"}}
	servers, err := runtimeMCP(req)
	want := []map[string]any{{"name": "fixture", "type": "http", "url": "https://mcp.example/mcp", "headers": []map[string]string{}}}
	if err != nil || !reflect.DeepEqual(servers, want) {
		t.Fatalf("MCP = %v, %v", servers, err)
	}
}

func TestPublicMCPStdioIsRejected(t *testing.T) {
	servers := []proto.MCPHTTPServer{{ConnectionOrigin: "environment", ServerLabel: "local", Stdio: &proto.MCPStdio{Command: "run", Cwd: "/work"}}}
	if _, err := runtimeMCP(proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{}, MCPHTTPServers: &servers}); err == nil {
		t.Fatal("native starts session stdio servers outside the declared cwd")
	}
}
