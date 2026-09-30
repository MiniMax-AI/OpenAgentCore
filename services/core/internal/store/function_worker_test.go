package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/google/uuid"
)

func TestWorkerWaitsForToolCapabilities(t *testing.T) {
	for _, missing := range []string{"preparation", "durable_input_receipts", "execution_controls", "function_tools", "tool_observations", "mcp_http_tools", "mcp_http_bearer_auth", "mcp_http_required"} {
		for _, prebound := range []bool{false, true} {
			t.Run(missing+"/"+map[bool]string{false: "select", true: "bound"}[prebound], func(t *testing.T) {
				h := newFunctionHarness(t)
				configuration := functionConfiguration
				isMCP := strings.HasPrefix(missing, "mcp_http_")
				token := ""
				if isMCP {
					configuration = mcpWorkerConfiguration
				}
				if missing == "mcp_http_bearer_auth" {
					configuration, token = mcpBearerWorkerConfiguration(t, h)
				}
				if missing == "mcp_http_required" {
					configuration = strings.Replace(configuration, `"required":false`, `"required":true`, 1)
				}
				if !prebound || isMCP {
					var err error
					h.session, err = h.s.CreateSession(t.Context(), h.tenant, sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "unbound", Configuration: []byte(configuration)})
					if err != nil {
						t.Fatal(err)
					}
				}
				if prebound && isMCP {
					if err := h.s.BindSessionDevice(t.Context(), h.tenant, h.session.ID, h.device.ID); err != nil {
						t.Fatal(err)
					}
				}
				caps := prototest.Capabilities(proto.AgentKindCapabilities{Preparation: proto.CapabilityFromBool(missing != "preparation"), Streaming: proto.CapabilitySupported, Steering: proto.CapabilitySupported, DurableTurns: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilityFromBool(missing != "durable_input_receipts"), EnvironmentNone: proto.CapabilitySupported, WebSearchControl: proto.CapabilitySupported, TextVerbosity: proto.CapabilitySupported, ExecutionControls: proto.CapabilityFromBool(missing != "execution_controls"), SubagentControl: proto.CapabilitySupported, ToolObservations: proto.CapabilityFromBool(missing != "tool_observations"), MCPHTTPTools: proto.CapabilityFromBool(missing != "mcp_http_tools"), MCPHTTPRequired: proto.CapabilityFromBool(missing != "mcp_http_required"), MCPHTTPBearerAuth: proto.CapabilityFromBool(missing != "mcp_http_bearer_auth"), FunctionTools: proto.CapabilityFromBool(missing != "function_tools" && !isMCP)})
				heartbeat := func() {
					h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
				}
				heartbeat()
				deadline := time.Now().Add(3 * time.Second)
				for {
					peer, _ := h.registry.LookupDevice(h.device.ID)
					info, _, _ := peer.AgentKindStatus("codex")
					if info.Capabilities.Preparation == caps.Preparation.IsSupported() && info.Capabilities.DurableInputReceipts == caps.DurableInputReceipts.IsSupported() && info.Capabilities.ExecutionControls == caps.ExecutionControls.IsSupported() && info.Capabilities.FunctionTools == caps.FunctionTools.IsSupported() && info.Capabilities.ToolObservations == caps.ToolObservations.IsSupported() && info.Capabilities.MCPHTTPTools == caps.MCPHTTPTools.IsSupported() && info.Capabilities.MCPHTTPBearerAuth == caps.MCPHTTPBearerAuth.IsSupported() && info.Capabilities.MCPHTTPRequired == caps.MCPHTTPRequired.IsSupported() {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("heartbeat not applied")
					}
					time.Sleep(10 * time.Millisecond)
				}
				input := h.message("queued", "Look up ticket")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				worker := startWorker(t, ctx, h.db, h.d)
				done := make(chan error, 1)
				go func() { done <- worker.Run(ctx) }()
				defer func() {
					cancel()
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Error("worker did not stop")
					}
				}()
				time.Sleep(650 * time.Millisecond)
				current, err := h.s.GetTurn(ctx, h.tenant, h.session.ID, input.TurnID)
				if err != nil || current.Status != sessions.TurnQueued {
					t.Fatal(current, err)
				}
				if !prebound {
					if _, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID); !errors.Is(err, sessions.ErrNotFound) {
						t.Fatal("bound an incapable device", err)
					}
				}
				caps.Preparation, caps.DurableInputReceipts, caps.ExecutionControls, caps.ToolObservations, caps.MCPHTTPTools = proto.CapabilitySupported, proto.CapabilitySupported, proto.CapabilitySupported, proto.CapabilitySupported, proto.CapabilitySupported
				caps.MCPHTTPBearerAuth, caps.FunctionTools, caps.MCPHTTPRequired = proto.CapabilitySupported, proto.CapabilityFromBool(!isMCP), proto.CapabilitySupported
				heartbeat()
				request := h.read(testExecutionRequest)
				var prompt proto.PromptRequestPayload
				if request.DecodePayload(&prompt) != nil {
					t.Fatal("invalid prompt")
				}
				if isMCP {
					if prompt.MCPHTTPServers == nil || len(*prompt.MCPHTTPServers) != 1 || (*prompt.MCPHTTPServers)[0].ServerLabel != "tickets" || (*prompt.MCPHTTPServers)[0].AllowedTools == nil || len(*(*prompt.MCPHTTPServers)[0].AllowedTools) != 0 || len(prompt.FunctionTools) != 0 {
						t.Fatal("MCP declaration lost during dispatch")
					}
					if (*prompt.MCPHTTPServers)[0].Required != (missing == "mcp_http_required") {
						t.Fatal("dispatch changed required initialization")
					}
					bearer := (*prompt.MCPHTTPServers)[0].BearerToken
					if token != "" && (bearer == nil || *bearer != token) || token == "" && bearer != nil {
						t.Fatal("dispatch lost selected authentication or authenticated an anonymous server")
					}
				} else if len(prompt.FunctionTools) != 1 || prompt.FunctionTools[0].Name != "lookup_ticket" {
					t.Fatal(prompt)
				}
				h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Content: "done"})
				waitTurn(t, h, input.TurnID, sessions.TurnCompleted)
			})
		}
	}
}

const mcpWorkerConfiguration = `{"agent":{"model":"gpt-5.5","tools":[{"type":"mcp","server_label":"tickets","transport":{"type":"http","server_url":"http://127.0.0.1:9191/mcp"},"connection_origin":"service","allowed_tools":[],"credential_id":null,"request_metadata":{},"required":false}]},"environment":{"type":"none"}}`

func mcpBearerWorkerConfiguration(t *testing.T, h *dispatchHarness) (string, string) {
	t.Helper()
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	h.s, h.db = store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	h.d.Store = h.s
	_, service, err := fixtureVaults(h.db)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := service.CreateVault(t.Context(), vaults.CreateVault{TenantID: h.tenant})
	if err != nil {
		t.Fatal(err)
	}
	token, endpoint := uuid.NewString(), "https://mcp.example/tools"
	_, err = service.CreateStaticCredential(t.Context(), vaults.CreateStaticCredential{TenantID: h.tenant, VaultID: vault.ID, Name: "worker", MCPServerURL: endpoint, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot execution.Snapshot
	if json.Unmarshal([]byte(strings.ReplaceAll(mcpWorkerConfiguration, "http://127.0.0.1:9191/mcp", endpoint)), &snapshot) != nil {
		t.Fatal("invalid worker fixture")
	}
	snapshot.VaultIDs = []string{vault.ID}
	snapshot.MCPCredentials, err = service.ResolveMCPCredentials(t.Context(), vaults.ResolveMCPCredentials{TenantID: h.tenant, VaultIDs: snapshot.VaultIDs,
		Requests: []vaults.MCPCredentialRequest{{ServerLabel: "tickets", ServerURL: endpoint}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(raw), token) {
		t.Fatal("unsafe worker configuration")
	}
	return string(raw), token
}
