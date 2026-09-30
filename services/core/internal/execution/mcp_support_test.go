package execution

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/google/uuid"
)

// recordingCredentials records each bearer-token lookup and answers with token.
type recordingCredentials struct {
	token    string
	requests []vaults.MCPBearerToken
}

func (c *recordingCredentials) MCPBearerToken(_ context.Context, command vaults.MCPBearerToken) (string, error) {
	c.requests = append(c.requests, command)
	return c.token, nil
}

func mcpSupportFixture(t *testing.T) (Snapshot, []proto.MCPHTTPServer, runtimedevice.KindCapabilities) {
	t.Helper()
	vault, credential := uuid.NewString(), uuid.NewString()
	tool := json.RawMessage(`{"type":"mcp","server_label":"tickets","connection_origin":"service","transport":{"type":"http","server_url":"https://mcp.example/tools"}}`)
	snapshot := Snapshot{Agent: v1.Agent{Model: "model", Tools: []json.RawMessage{tool}}, Environment: &v1.Environment{Type: "none"}, VaultIDs: []string{vault},
		MCPCredentials: []vaults.MCPCredentialBinding{{ServerLabel: "tickets", ServerURL: "https://mcp.example/tools", VaultID: vault, CredentialID: credential, AuthType: "static_bearer"}}}
	tools, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		t.Fatal(err)
	}
	caps := runtimedevice.KindCapabilities{EnvironmentNone: true, MCPHTTPTools: true, MCPHTTPBearerAuth: true, MCPHTTPRequired: true,
		Preparation: true}
	return snapshot, tools.MCP, caps
}

func TestMCPPublicBearerPolicyIsIndependentOfRuntimeCapabilities(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk", "unknown"} {
		t.Run(engine, func(t *testing.T) {
			snapshot, servers, caps := mcpSupportFixture(t)
			raw, _ := json.Marshal(snapshot)
			allowed := engine == "codex" || engine == "claude_sdk"
			if err := (Policy{}).ValidateSessionConfiguration(engine, raw); (err == nil) != allowed {
				t.Fatal("creation bypassed public credential policy", err)
			}
			if (Policy{}).canAdmitInputs(engine, raw) != allowed {
				t.Fatal("later input bypassed public credential policy")
			}
			if _, err := (Policy{}).mcpExecutionCredentials(engine, snapshot, servers, caps); (err == nil) != allowed {
				t.Fatal("runtime capabilities widened public admission", err)
			}
			credentials := &recordingCredentials{token: "scoped-token"}
			session := store.Session{TenantID: uuid.NewString(), Engine: engine}
			request, err := (&Dispatcher{Credentials: credentials}).executionRequest(t.Context(), session, snapshot, caps, store.SessionExecutionBinding{})
			if !allowed {
				if err == nil || err.Error() != "The configured engine does not support this MCP connection origin." || request.MCPHTTPServers != nil || len(credentials.requests) != 0 {
					t.Fatal("unverified profile bypassed public policy", err)
				}
				return
			}
			// The lookup carries exactly the Session's tenant, attached Vaults and frozen binding.
			want := []vaults.MCPBearerToken{{TenantID: session.TenantID, VaultIDs: snapshot.VaultIDs, Binding: snapshot.MCPCredentials[0]}}
			if err != nil || !reflect.DeepEqual(credentials.requests, want) {
				t.Fatal("accepted profile did not reach scoped credential lookup", err, credentials.requests)
			}
			if servers := request.MCPHTTPServers; servers == nil || len(*servers) != 1 || (*servers)[0].BearerToken == nil || *(*servers)[0].BearerToken != "scoped-token" {
				t.Fatal("looked-up token did not reach its server")
			}
		})
	}
}

func TestMCPExecutionChecksRequireVerifiedCapabilityCombinations(t *testing.T) {
	for _, placement := range []string{"none", "self_hosted"} {
		for _, missing := range []string{"", "mcp", "bearer", "placement", "required", "preparation", "environment"} {
			t.Run(placement+"/"+missing, func(t *testing.T) {
				snapshot, servers, caps := mcpSupportFixture(t)
				snapshot.Environment.Type = placement
				snapshot.Environment.WorkspaceDirectory = "/work"
				servers[0].Required = true
				var tool v1.MCPTool
				if err := json.Unmarshal(snapshot.Agent.Tools[0], &tool); err != nil {
					t.Fatal(err)
				}
				tool.Required = true
				snapshot.Agent.Tools[0], _ = json.Marshal(tool)
				switch missing {
				case "mcp":
					caps.MCPHTTPTools = false
				case "bearer":
					caps.MCPHTTPBearerAuth = false
				case "placement":
					caps.EnvironmentNone = false
				case "required":
					caps.MCPHTTPRequired = false
				case "preparation":
					caps.Preparation = false
				case "environment":
					snapshot.Environment = nil
				}
				allowed := placement == "none" && (missing == "" || missing == "preparation")
				selected, err := (Policy{}).mcpExecutionCredentials("codex", snapshot, servers, caps)
				if (err == nil) != allowed || allowed && len(selected) != 1 {
					t.Fatal("incorrect combined MCP capability decision", err)
				}
				if !allowed {
					credentials := &recordingCredentials{token: "scoped-token"}
					request, requestErr := (&Dispatcher{Credentials: credentials}).executionRequest(t.Context(), store.Session{Engine: "codex"}, snapshot, caps, store.SessionExecutionBinding{})
					if requestErr == nil || request.MCPHTTPServers != nil || len(credentials.requests) != 0 {
						t.Fatal("request bypassed capability checks before credential lookup", requestErr)
					}
				}
			})
		}
	}
}

func TestMCPAnonymousExecutionPreservesFrozenDecision(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk"} {
		for _, mode := range []string{"unattached", "frozen anonymous", "invalid binding"} {
			t.Run(engine+"/"+mode, func(t *testing.T) {
				snapshot, _, caps := mcpSupportFixture(t)
				caps.MCPHTTPBearerAuth = false
				if mode == "unattached" {
					snapshot.VaultIDs, snapshot.MCPCredentials = nil, nil
				} else {
					snapshot.MCPCredentials[0].VaultID = ""
					snapshot.MCPCredentials[0].CredentialID = ""
					snapshot.MCPCredentials[0].AuthType = ""
					if mode == "invalid binding" {
						snapshot.MCPCredentials[0].ServerURL += "/different"
					}
				}
				raw, _ := json.Marshal(snapshot)
				allowed := mode != "invalid binding"
				if err := (Policy{}).ValidateSessionConfiguration(engine, raw); (err == nil) != allowed || (Policy{}).canAdmitInputs(engine, raw) != allowed {
					t.Fatal("anonymous binding validation changed", err)
				}
				credentials := &recordingCredentials{token: "scoped-token"}
				request, err := (&Dispatcher{Credentials: credentials}).executionRequest(t.Context(), store.Session{Engine: engine}, snapshot, caps, store.SessionExecutionBinding{})
				if len(credentials.requests) != 0 {
					t.Fatal("anonymous or invalid binding reached credential lookup")
				}
				if !allowed {
					if err == nil || request.MCPHTTPServers != nil {
						t.Fatal("invalid frozen binding reached dispatch")
					}
					return
				}
				if err != nil || request.MCPHTTPServers == nil || len(*request.MCPHTTPServers) != 1 || (*request.MCPHTTPServers)[0].BearerToken != nil {
					t.Fatal("anonymous dispatch gained a credential requirement", err)
				}
			})
		}
	}
}
