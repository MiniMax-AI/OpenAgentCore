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

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentplugin"
)

func environmentMCPFixture() proto.EnvironmentMCP {
	return proto.EnvironmentMCP{PackageRoot: "plugins/fixture", Server: agentplugin.MCPServer{
		Name: "proof.server", Type: "stdio", Command: "never-exec-before-sandbox", Args: []string{"private-argument"},
		EnvVars: []string{"USER_SELECTED"}, CWD: "resources",
	}}
}

func TestEnvironmentMCPUsesFixedLauncherForNewAndLoadedSessions(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "load"}[resume], func(t *testing.T) {
			c, req, record := workspaceFixture(t)
			c.Network, req.LocalEnvironment.NetworkAccess = "enabled", "enabled"
			req.LocalEnvironment.MCP = []proto.EnvironmentMCP{environmentMCPFixture()}
			if resume {
				req.AgentSessionID = "native-1"
			}
			t.Setenv("USER_SELECTED", "must-not-resolve-from-daemon")
			t.Setenv("MODEL_SECRET", "must-not-forward")
			resource, err := NewPreparationFactory(c)(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = resource.Close() })
			raw, err := os.ReadFile(record + ".session")
			if err != nil {
				t.Fatal(err)
			}
			var params struct {
				SessionID string `json:"sessionId"`
				MCP       []struct {
					Name, Command string
					Args          []string
					Env           []map[string]string
				} `json:"mcpServers"`
			}
			if json.Unmarshal(raw, &params) != nil || len(params.MCP) != 2 || params.MCP[0].Name != "parsar_workspace" {
				t.Fatal("environment MCP displaced workspace tools")
			}
			server := params.MCP[1]
			if server.Name != "proof.server" || server.Command != "/usr/bin/python3" || server.Env == nil || len(server.Env) != 0 ||
				!reflect.DeepEqual(server.Args, []string{"-I", "-S", "/usr/local/bin/agents-api-runtime-initialize", "stdio", "plugins/fixture", "proof.server"}) {
				t.Fatal("ACP declaration bypassed the fixed isolated launcher")
			}
			if (params.SessionID != "") != resume || strings.Contains(string(raw), "must-not") || strings.Contains(string(raw), "private-argument") {
				t.Fatal("native attachment changed identity or exposed private inputs")
			}
		})
	}
}

func TestEnvironmentMCPRejectsUnqualifiedAuthorityBeforePreparation(t *testing.T) {
	for _, name := range []string{"http", "http-bearer", "restricted", "disabled", "duplicate", "reserved"} {
		t.Run(name, func(t *testing.T) {
			c, req, _ := workspaceFixture(t)
			c.Network, req.LocalEnvironment.NetworkAccess = "enabled", "enabled"
			req.LocalEnvironment.MCP = []proto.EnvironmentMCP{environmentMCPFixture()}
			switch name {
			case "http", "http-bearer":
				req.LocalEnvironment.MCP[0].Server = agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.invalid/mcp"}
				if name == "http-bearer" {
					token := "confidential-http-token"
					req.LocalEnvironment.MCP[0].BearerToken = &token
				}
			case "restricted", "disabled":
				c.Network, req.LocalEnvironment.NetworkAccess = name, name
			case "duplicate":
				req.LocalEnvironment.MCP = append(req.LocalEnvironment.MCP, environmentMCPFixture())
			case "reserved":
				req.LocalEnvironment.MCP[0].Server.Name = "parsar_workspace"
			}
			if _, err := prepareWorkspaceOptions(t.Context(), c, req); err == nil || strings.Contains(err.Error(), "confidential-http-token") {
				t.Fatal("unqualified declaration accepted or credential exposed")
			}
		})
	}
}

func TestEnvironmentMCPCancelSettlesPendingObservationBeforeDone(t *testing.T) {
	c, req, _ := workspaceFixture(t)
	c.Network, req.LocalEnvironment.NetworkAccess = "enabled", "enabled"
	req.LocalEnvironment.MCP = []proto.EnvironmentMCP{environmentMCPFixture()}
	req.ObserveToolObservations = true
	script, err := os.ReadFile(c.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Binary, []byte(strings.Replace(string(script), "HELPER=prepared", "HELPER=prepared-mcp-cancel", 1)), 0700); err != nil {
		t.Fatal(err)
	}
	resource, err := NewPreparationFactory(c)(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out := make(chan proto.Envelope, 16)
	session, err := resource.Start(ctx, "run", proto.TextInput("invoke and wait"), out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Cancel(context.Background()) })
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
		t.Fatal(err)
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
