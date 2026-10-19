package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestPublicMCPHTTPEffectiveConfiguration(t *testing.T) {
	for _, allowlist := range []*[]string{nil, new([]string), {"lookup", "query"}} {
		servers := map[string]mcpServerConfig{"docs": {URL: "https://docs.example/mcp", EnabledTools: allowlist}}
		// Only what binds a credential to the declared server is checked; every
		// other native setting and undeclared server keeps its native value.
		for _, mutation := range []string{"none", "ambient", "url", "header", "header helper", "env header", "auth", "required", "tools", "disabled", "remote", "plugins", "apps", "keyring", "policy", "timeout"} {
			t.Run(mutation+"/"+allowlistName(allowlist), func(t *testing.T) {
				response := mcpHTTPConfigResponse(servers)
				config := response["config"].(map[string]any)
				entries := config["mcp_servers"].(map[string]any)
				server := entries["docs"].(map[string]any)
				switch mutation {
				case "ambient":
					entries["operator"] = map[string]any{"command": "operator-mcp", "enabled": true}
				case "url":
					server["url"] = "https://other.example/mcp"
				case "header":
					server["http_headers"] = map[string]any{"Authorization": "synthetic-private"}
				case "header helper":
					server["http_headers_helper"] = "operator-credentials"
				case "env header":
					server["env_http_headers"] = map[string]any{"Authorization": "OPERATOR_SECRET"}
				case "auth":
					server["auth"] = "chatgpt"
				case "required":
					server["required"] = true
				case "tools":
					server["enabled_tools"] = []string{"undeclared"}
				case "disabled":
					server["enabled"] = false
				case "remote":
					server["environment_id"] = "remote"
				case "plugins", "apps":
					config["features"] = map[string]any{mutation: true}
				case "keyring":
					config["mcp_oauth_credentials_store"] = "auto"
				case "policy":
					server["tools"] = map[string]any{"lookup": map[string]any{"enabled": false}}
				case "timeout":
					server["tool_timeout_sec"] = 5
				}
				data, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				rejected := map[string]bool{"url": true, "header": true, "env header": true, "remote": true, "keyring": true, "tools": allowlist != nil}
				if matchesMCPConfig(data, servers) == rejected[mutation] {
					t.Fatal("effective configuration decision differed", mutation)
				}
			})
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"config":{"mcp_servers":[]}}`, `not json`} {
		if matchesMCPConfig(json.RawMessage(raw), map[string]mcpServerConfig{}) {
			t.Fatal("missing or malformed native proof accepted")
		}
	}
	empty := map[string]mcpServerConfig{}
	data, _ := json.Marshal(mcpHTTPConfigResponse(empty))
	if !matchesMCPConfig(data, empty) {
		t.Fatal("explicit empty declaration rejected")
	}
}

func allowlistName(tools *[]string) string {
	if tools == nil {
		return "all"
	}
	if len(*tools) == 0 {
		return "none"
	}
	return "selected"
}

func TestPublicMCPHTTPRequiredConfigurationCannotBeWeakened(t *testing.T) {
	servers := map[string]mcpServerConfig{"docs": {URL: "https://docs.example/mcp", Required: true}}
	for _, value := range []any{true, false, nil, "true", "omitted"} {
		response := mcpHTTPConfigResponse(servers)
		server := response["config"].(map[string]any)["mcp_servers"].(map[string]any)["docs"].(map[string]any)
		server["required"] = value
		if value == "omitted" {
			delete(server, "required")
		}
		raw, err := json.Marshal(response)
		if err != nil || matchesMCPConfig(raw, servers) != (value == true) {
			t.Fatal("required initialization was weakened or rejected", value, err)
		}
	}
}

func TestPublicMCPHTTPPreflightRedactsNativeErrors(t *testing.T) {
	client, server, cleanup := NewTestClient()
	defer cleanup()
	done := make(chan error, 1)
	go func() {
		done <- verifyMCPConfig(t.Context(), client.JSONRPCClient, SessionPlan{Cwd: "/private/workspace"})
	}()
	var req struct {
		ID     string         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(server.FromClient).Decode(&req); err != nil {
		t.Fatal(err)
	}
	if req.Method != "config/read" || req.Params["cwd"] != "/private/workspace" || req.Params["includeLayers"] != false {
		t.Fatal("preflight did not request exact cwd configuration")
	}
	if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32603, "message": "synthetic-secret"}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("native error was accepted or exposed", err)
	}
}

func TestPublicMCPHTTPPreparationChecksBeforeNewAndResumedThread(t *testing.T) {
	for _, mode := range []string{"new", "resume", "extra server", "reject url", "reject bearer reference"} {
		t.Run(mode, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			req.AgentOptions = map[string]any{"model": "fixture-model"}
			servers := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "docs", ServerURL: "https://docs.example/mcp"}}
			if mode == "reject bearer reference" {
				token := "synthetic-private-bearer"
				servers[0].BearerToken = &token
			}
			req.MCPHTTPServers = &servers
			if mode == "resume" {
				req.AgentSessionID = "fixture-native-thread"
			}
			t.Setenv("OAC_TEST_PREPARATION_STATUS", filepath.Join(root, "unknown-status"))
			if err := os.WriteFile(filepath.Join(root, "unknown-status"), []byte("unknown"), 0o600); err != nil {
				t.Fatal(err)
			}
			declarations, _, err := runtimeMCPServers(req)
			if err != nil {
				t.Fatal(err)
			}
			response := mcpHTTPConfigResponse(declarations)
			if mode == "extra server" {
				response["config"].(map[string]any)["mcp_servers"].(map[string]any)["operator"] = map[string]any{"url": "https://operator.example/private"}
			}
			if mode == "reject url" {
				response["config"].(map[string]any)["mcp_servers"].(map[string]any)["docs"].(map[string]any)["url"] = "https://workspace.example/redirect"
			}
			if mode == "reject bearer reference" {
				response["config"].(map[string]any)["mcp_servers"].(map[string]any)["docs"].(map[string]any)["bearer_token_env_var"] = "OPERATOR_SECRET"
			}
			path := filepath.Join(root, "native-config.json")
			writeMCPHTTPConfigResponse(t, path, response)
			t.Setenv("OAC_TEST_PREPARATION_MCP_CONFIG", path)
			e, err := testExecutor(t, "complete", req, cfg)
			if strings.HasPrefix(mode, "reject") {
				if err == nil || e != nil {
					t.Fatal("redirected credential binding admitted")
				}
				assertPreparationOnly(t, root)
				waitPreparationMethod(t, root, "config/read")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertPreparationOnly(t, root)
			home, err := allocCodexHome(testStateRoot(t), req.AgentStateKey)
			if err != nil {
				t.Fatal(err)
			}
			out := make(chan proto.Envelope, 20)
			turn, err := e.StartTurn(t.Context(), "actual-run", proto.TextInput("actual prompt"), out)
			if err != nil {
				t.Fatal(err)
			}
			awaitExecutorTurn(t, turn, out)
			frames := preparationFrames(t, root)
			checked, statuses := false, 0
			for _, frame := range frames {
				if frame.Method == "environment/status" {
					statuses++
				}
				if frame.Method == "config/read" {
					if statuses != 2 {
						t.Fatal("MCP check preceded disabled-environment confirmation")
					}
					checked = true
				}
				if strings.HasPrefix(frame.Method, "thread/") {
					var params map[string]any
					if err := json.Unmarshal(frame.Params, &params); err != nil {
						t.Fatal(err)
					}
					if !checked || params["cwd"] != home || (frame.Method == "thread/resume") != (mode == "resume") {
						t.Fatal("thread started before the check or with another cwd")
					}
				}
				if frame.Method == "mcpServerStatus/list" {
					t.Fatal("preflight started discovery")
				}
			}
		})
	}
}

func TestPublicMCPStdioReachesNativeConfiguration(t *testing.T) {
	tools := []string{"lookup"}
	servers := []proto.MCPHTTPServer{{ConnectionOrigin: "environment", ServerLabel: "local", AllowedTools: &tools,
		Stdio: &proto.MCPStdio{Command: "run", Args: []string{"-x"}, Cwd: "/work", EnvVars: []string{"TOKEN"}}}}
	declared, _, err := runtimeMCPServers(proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{}, MCPHTTPServers: &servers})
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := writeCodexMCPConfig(home, declared); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`command = "run"`, `args = ["-x"]`, `cwd = "/work"`, `env_vars = ["TOKEN"]`, `enabled_tools = ["lookup"]`} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("missing native config %q", expected)
		}
	}
	read := func(cwd string) json.RawMessage {
		return json.RawMessage(`{"config":{"mcp_oauth_credentials_store":"file","mcp_servers":{"local":{"environment_id":"local","command":"run","args":["-x"],"cwd":"` + cwd + `","env_vars":["TOKEN"],"enabled_tools":["lookup"]}}}}`)
	}
	if !matchesMCPConfig(read("/work"), declared) || matchesMCPConfig(read("/elsewhere"), declared) {
		t.Fatal("effective native stdio configuration was not verified")
	}
}
