package dispatch_test

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

func TestMCPHTTPBearerRejectsUnsupportedRequestsBeforeFactory(t *testing.T) {
	for _, mode := range []string{"supported", "claude", "claude old peer", "claude local", "no bearer capability", "no MCP capability", "unavailable", "no none capability", "local", "other engine", "HTTP", "credential-free", "product", "required", "required old peer", "optional old peer"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			token := "synthetic-private-token"
			servers := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "tools", ServerURL: "https://tools.example/mcp", BearerToken: &token}}
			req := proto.PromptRequestPayload{AgentKind: "codex", DisableExecutionEnvironment: true, MCPHTTPServers: &servers}
			caps := prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported, MCPHTTPTools: proto.CapabilitySupported, MCPHTTPBearerAuth: proto.CapabilitySupported})
			switch mode {
			case "claude", "claude old peer", "claude local":
				req.AgentKind = "claude_sdk"
				caps.MCPHTTPBearerAuth = proto.CapabilityFromBool(mode != "claude old peer")
				if mode == "claude local" {
					req.DisableExecutionEnvironment = false
				}
			case "required", "required old peer", "optional old peer":
				servers[0].Required = mode != "optional old peer"
				servers[0].BearerToken = nil
				caps.MCPHTTPRequired = proto.CapabilityFromBool(mode == "required")
			case "no bearer capability", "credential-free", "product":
				caps.MCPHTTPBearerAuth = proto.CapabilityUnsupported
			case "no MCP capability":
				caps.MCPHTTPTools = proto.CapabilityUnsupported
			case "no none capability":
				caps.EnvironmentNone = proto.CapabilityUnsupported
			case "local":
				req.DisableExecutionEnvironment = false
			case "other engine":
				req.AgentKind = "other"
			case "HTTP":
				servers[0].ServerURL = "http://tools.example/mcp"
			}
			if mode == "credential-free" {
				servers[0].BearerToken = nil
			}
			if mode == "product" {
				req.MCPHTTPServers, req.DisableExecutionEnvironment = nil, false
			}
			called := false
			h.reg.RegisterKind(proto.SupportedAgentKind{Kind: req.AgentKind, Available: mode != "unavailable", Capabilities: caps},
				harnessconfig.Configuration{}, func(_ context.Context, got proto.PromptRequestPayload, _ chan<- proto.Envelope) (agent.Session, error) {
					called = true
					if mode == "required" && !(*got.MCPHTTPServers)[0].Required {
						t.Error("required initialization lost before adapter")
					}
					if (mode == "supported" || mode == "claude") && (got.MCPHTTPServers == nil || (*got.MCPHTTPServers)[0].BearerToken == nil || *(*got.MCPHTTPServers)[0].BearerToken != token) {
						t.Error("token lost before adapter")
					}
					return nil, errors.New("controlled factory stop")
				})
			err := h.router.Handle(t.Context(), mustEnv(t, proto.TypePromptRequest, "mcp-bearer", req))
			if called != (mode == "supported" || mode == "claude" || mode == "other engine" || mode == "credential-free" || mode == "product" || mode == "required" || mode == "optional old peer") {
				t.Fatal("wrong factory admission")
			}
			frames := h.sender.snapshot()
			raw, _ := json.Marshal(frames)
			if err == nil || strings.Contains(err.Error(), token) || strings.Contains(string(raw), token) || len(frames) != 2 || frames[0].Type != proto.TypeError || frames[1].Type != proto.TypeDone {
				t.Fatal("terminal rejection missing or exposed credential")
			}
		})
	}
}

func TestLocalMCPOriginAndCapabilityAdmission(t *testing.T) {
	for _, mode := range []string{"anonymous", "bearer", "required", "empty declaration", "no declaration", "environment anonymous", "environment bearer", "environment required", "environment missing capability"} {
		t.Run(mode, func(t *testing.T) {
			h := localPreparationHarness(t)
			defer h.router.Shutdown(context.Background())
			req := preparationRequest()
			servers := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "tools", ServerURL: "https://tools.example/mcp"}}
			req.Configuration.MCPHTTPServers = &servers
			if strings.HasPrefix(mode, "environment") {
				servers[0].ConnectionOrigin = "environment"
				req.Configuration.LocalEnvironment.NetworkAccess = "enabled"
			}
			if strings.Contains(mode, "bearer") {
				token := "synthetic-private-token"
				servers[0].BearerToken = &token
			}
			if strings.Contains(mode, "required") {
				servers[0].Required = true
			}
			if mode == "empty declaration" {
				servers = []proto.MCPHTTPServer{}
			}
			if mode == "no declaration" {
				req.Configuration.MCPHTTPServers = nil
			}
			entered := make(chan struct{}, 1)
			h.reg.RegisterKind(proto.SupportedAgentKind{Kind: "prepared", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported, MCPHTTPTools: proto.CapabilityFromBool(mode != "environment missing capability"), MCPHTTPBearerAuth: proto.CapabilitySupported, MCPHTTPRequired: proto.CapabilitySupported})}, harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
				t.Error("ordinary factory called")
				return nil, errors.New("unexpected")
			})
			h.reg.RegisterExecutor("prepared", func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
				entered <- struct{}{}
				return nil, errors.New("controlled stop")
			})
			err := h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "local-mcp", req))
			allowed := mode == "no declaration" || mode == "empty declaration" || strings.HasPrefix(mode, "environment") && mode != "environment missing capability"
			if (err == nil) != allowed {
				t.Fatal("wrong preparation admission", err)
			}
			if allowed {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("factory not called")
				}
				waitPreparationStatus(t, h.sender, "local-mcp", "failed", "")
			} else {
				select {
				case <-entered:
					t.Fatal("rejected request reached factory")
				default:
				}
			}
		})
	}
}
