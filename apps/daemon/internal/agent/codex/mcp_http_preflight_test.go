package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestPublicMCPHTTPEffectiveConfiguration(t *testing.T) {
	for _, allowlist := range []*[]string{nil, new([]string), {"lookup", "query"}} {
		servers := map[string]mcpServerConfig{"docs": {URL: "https://docs.example/mcp", EnabledTools: allowlist}}
		for _, mutation := range []string{"none", "ambient", "url", "header", "header helper", "env header", "bearer", "auth", "required", "tools", "disabled", "remote", "plugins", "apps", "keyring", "policy"} {
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
				case "bearer":
					server["bearer_token_env_var"] = "OPERATOR_SECRET"
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
					config["features"].(map[string]any)[mutation] = true
				case "keyring":
					config["mcp_oauth_credentials_store"] = "auto"
				case "policy":
					server["tools"] = map[string]any{"lookup": map[string]any{"enabled": false}}
				}
				data, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				if matchesMCPConfig(data, servers) != (mutation == "none") {
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

func TestPublicMCPHTTPPreflightUsesDefaultRequestBudget(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	req.ExecutionControls = nil
	t.Setenv("OAC_TEST_PREPARATION_MCP_DELAY", "6s")
	if _, err := testExecutor(t, "complete", req, cfg); err != nil {
		t.Fatalf("valid MCP configuration after remote metadata work: %v", err)
	}
	assertPreparationOnly(t, root)
	waitPreparationMethod(t, root, "config/read")
}

func TestPublicMCPHTTPPreflightBudgetFailuresStayRedacted(t *testing.T) {
	for _, mode := range []string{"request-timeout", "owner-cancel"} {
		t.Run(mode, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "request-timeout" {
				client.cfg.RequestTimeout = 100 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() {
				done <- verifyMCPConfig(ctx, client.JSONRPCClient, SessionPlan{Cwd: "/synthetic-private/workspace"})
			}()
			var request JsonRpcRequest
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Method != "config/read" {
				t.Fatal("unexpected preflight request", request.Method)
			}
			if mode == "owner-cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil || err.Error() != "codex: cannot verify public MCP configuration" {
					t.Fatal("budget failure was accepted or exposed", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("preflight ignored its configured budget or parent cancellation")
			}
			if mode == "request-timeout" && ctx.Err() != nil {
				t.Fatal("request budget changed the parent context", ctx.Err())
			}
		})
	}
}

func TestPublicMCPHTTPPreparationChecksBeforeNewAndResumedThread(t *testing.T) {
	for _, mode := range []string{"new", "resume", "reject", "reject bearer reference"} {
		t.Run(mode, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			req.ExecutionControls = nil
			cfg.view.MCP = []agent.MCPBinding{{ServerLabel: "docs", ConnectionOrigin: "service", CredentialAuthority: "project_vault", Transport: "http", ServerURL: "http://127.0.0.1:17102/mcp"}}
			if mode == "resume" {
				req.AgentSessionID = "fixture-native-thread"
			}
			t.Setenv("OAC_TEST_PREPARATION_STATUS", filepath.Join(root, "unknown-status"))
			if err := os.WriteFile(filepath.Join(root, "unknown-status"), []byte("unknown"), 0o600); err != nil {
				t.Fatal(err)
			}
			declarations := mcpServersFromBindings(cfg.view.MCP)
			response := mcpHTTPConfigResponse(declarations)
			if mode == "reject" {
				response["config"].(map[string]any)["mcp_servers"].(map[string]any)["operator"] = map[string]any{"url": "https://operator.example/private"}
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
					t.Fatal("ambient MCP configuration admitted")
				}
				assertPreparationOnly(t, root)
				waitPreparationMethod(t, root, "config/read")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertPreparationOnly(t, root)
			work := filepath.Join(root, "home", agent.ViewWorkName)
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
					if !checked || params["cwd"] != work || (frame.Method == "thread/resume") != (mode == "resume") {
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
