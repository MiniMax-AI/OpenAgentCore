package execution

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
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

func mcpSupportFixture(t *testing.T) Snapshot {
	t.Helper()
	vault, credential := uuid.NewString(), uuid.NewString()
	tool := json.RawMessage(`{"type":"mcp","server_label":"tickets","connection_origin":"service","transport":{"type":"http","server_url":"https://mcp.example/tools"}}`)
	snapshot := Snapshot{ModelProviderConfigured: true, Agent: v1.Agent{Model: "model", Tools: []json.RawMessage{tool}}, Environment: &v1.Environment{Type: "none"}, VaultIDs: []string{vault},
		MCPCredentials: []vaults.MCPCredentialBinding{{ServerLabel: "tickets", ServerURL: "https://mcp.example/tools", VaultID: vault, CredentialID: credential, AuthType: "static_bearer"}}}
	return snapshot
}

func TestMCPBearerLookupUsesTheFrozenBinding(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk", "unknown"} {
		t.Run(engine, func(t *testing.T) {
			snapshot := mcpSupportFixture(t)
			raw, _ := json.Marshal(snapshot)
			allowed := engine == "codex" || engine == "claude_sdk"
			if err := ValidateSessionConfiguration(engine, raw); (err == nil) != allowed {
				t.Fatal("creation bypassed public credential policy", err)
			}
			if canAdmitInputs(engine, raw) != allowed {
				t.Fatal("later input bypassed public credential policy")
			}
			if !allowed {
				return
			}
			credentials := &recordingCredentials{token: "scoped-token"}
			session := sessions.Session{TenantID: uuid.NewString(), Engine: engine}
			request, err := (&Dispatcher{SessionsReader: frozenProvider{engine: engine}, Credentials: credentials}).executionRequest(t.Context(), session, snapshot, proto.Declaration{}, sessions.ExecutionBinding{})
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

func TestMCPAnonymousExecutionPreservesFrozenDecision(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk"} {
		for _, mode := range []string{"unattached", "frozen anonymous", "invalid binding"} {
			t.Run(engine+"/"+mode, func(t *testing.T) {
				snapshot := mcpSupportFixture(t)
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
				if err := ValidateSessionConfiguration(engine, raw); (err == nil) != allowed || canAdmitInputs(engine, raw) != allowed {
					t.Fatal("anonymous binding validation changed", err)
				}
				if selection, err := harnessSelection(snapshot); allowed && (err != nil || selection.MCP[0].Bearer) {
					t.Fatal("anonymous server selected bearer authentication", err)
				}
				credentials := &recordingCredentials{token: "scoped-token"}
				request, err := (&Dispatcher{SessionsReader: frozenProvider{engine: engine}, Credentials: credentials}).executionRequest(t.Context(), sessions.Session{Engine: engine}, snapshot, proto.Declaration{}, sessions.ExecutionBinding{})
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
