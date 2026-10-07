package agent_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestViewValidate(t *testing.T) {
	if err := validView(t).Validate(); err != nil {
		t.Fatalf("valid view: %v", err)
	}
	for name, change := range map[string]func(*agent.View){
		"nil executor":                    func(v *agent.View) { v.Executor = nil },
		"unset proxy":                     func(v *agent.View) { v.Proxy = 0 },
		"reserved closure name":           func(v *agent.View) { v.Closure[0].Name = agent.ViewHomeName },
		"relative closure directory":      func(v *agent.View) { v.Closure[0].HostDir = "opt/harness" },
		"local exec outside the closure":  func(v *agent.View) { v.LocalExec = append(v.LocalExec, "/usr/bin/node") },
		"local exec in read-only overlay": func(v *agent.View) { v.LocalExec = append(v.LocalExec, "/etc/ssl/certs/tool") },
		"mask inside an overlay": func(v *agent.View) {
			v.Masks = append(v.Masks, agent.ViewMask{Path: "/etc/ssl/certs/private", Dir: true})
		},
		"shim path equal to a mask":   func(v *agent.View) { v.ShimPaths = append(v.ShimPaths, "/etc/harness") },
		"overlay in the private root": func(v *agent.View) { v.Overlays[1].Path = "/.oac/certs" },
		"mask in /proc":               func(v *agent.View) { v.Masks[0].Path = "/proc/cpuinfo" },
		"unclean view path":           func(v *agent.View) { v.Masks[0].Path = "/etc/../etc/harness" },
		"duplicate shim":              func(v *agent.View) { v.Shims = append(v.Shims, "git") },
		"shim named as the relay":     func(v *agent.View) { v.Shims = append(v.Shims, agent.ViewRelayName) },
		"forwarded assignment":        func(v *agent.View) { v.ForwardEnv = append(v.ForwardEnv, "A=B") },
		"forwarded broker variable":   func(v *agent.View) { v.ForwardEnv = append(v.ForwardEnv, "PATH") },
		"forwarded proxy variable":    func(v *agent.View) { v.ForwardEnv = append(v.ForwardEnv, "https_proxy") },
	} {
		view := validView(t)
		change(&view)
		if err := view.Validate(); !errors.Is(err, agent.ErrInvalidView) {
			t.Errorf("%s: Validate = %v, want ErrInvalidView", name, err)
		}
	}
}

func TestRegistryResolvesOnlyDeclaredViews(t *testing.T) {
	reg := agent.NewRegistry()
	declared := validView(t)
	for kind, view := range map[string]*agent.View{"with_view": &declared, "without_view": nil} {
		info := proto.SupportedAgentKind{Kind: kind, Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}
		reg.Register(agent.Declaration{Info: info}, agent.Runtime{Info: info, Session: stubFactory(kind), View: view})
	}
	if _, err := reg.ResolveView("without_view"); !errors.Is(err, agent.ErrUnsupportedOperation) {
		t.Fatalf("ResolveView without a view = %v, want ErrUnsupportedOperation", err)
	}
	view, err := reg.ResolveView("with_view")
	if err != nil || !slices.Equal(view.LocalExec, declared.LocalExec) || view.Executor == nil {
		t.Fatalf("ResolveView = %+v, %v; want the declared view", view, err)
	}
}

func TestViewExecutorReceivesOnlyGatewayConnections(t *testing.T) {
	reg := agent.NewRegistry()
	declared := validView(t)
	declared.Executor = func(context.Context, proto.PromptRequestPayload, agent.ViewSession) (agent.Executor, error) {
		return nil, errReached
	}
	info := proto.SupportedAgentKind{Kind: "viewed", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}
	declaration := agent.Declaration{
		Info:          info,
		Configuration: harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: string(modelprovider.Responses)}}},
	}
	reg.Register(declaration, agent.Runtime{Info: info, Session: stubFactory("viewed"), View: &declared})
	view, err := reg.ResolveView("viewed")
	if err != nil {
		t.Fatal(err)
	}
	request := func(baseURL, key string) proto.PromptRequestPayload {
		return proto.PromptRequestPayload{Model: "m", ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: baseURL, APIKey: key}}
	}
	gatewayRequest := request("http://127.0.0.1:4101", modelprovider.Placeholder)
	requestMCP, installedMCP := gatewayRequest, gatewayRequest
	requestMCP.MCPHTTPServers = &[]proto.MCPHTTPServer{}
	installedMCP.LocalEnvironment = &proto.LocalEnvironment{MCP: []proto.EnvironmentMCP{{}}}
	token := "secret"
	gateway := agent.MCPBinding{ServerLabel: "docs", Transport: "http", ServerURL: "http://127.0.0.1:4100/mcp/docs"}
	withBearer, withHeaders, remote := gateway, gateway, gateway
	withBearer.BearerToken = &token
	withHeaders.HTTPHeaders = map[string]string{"X-Api-Key": token}
	remote.ServerURL = "https://mcp.example.com/docs"
	for name, c := range map[string]struct {
		req proto.PromptRequestPayload
		mcp []agent.MCPBinding
	}{
		"gateway":            {req: gatewayRequest, mcp: []agent.MCPBinding{gateway}},
		"model key":          {req: request("http://127.0.0.1:4101", token)},
		"model endpoint":     {req: request("https://api.example.com", modelprovider.Placeholder)},
		"request MCP":        {req: requestMCP},
		"installed MCP":      {req: installedMCP},
		"bearer":             {req: gatewayRequest, mcp: []agent.MCPBinding{withBearer}},
		"credential headers": {req: gatewayRequest, mcp: []agent.MCPBinding{withHeaders}},
		"direct MCP":         {req: gatewayRequest, mcp: []agent.MCPBinding{remote}},
	} {
		want := agent.ErrViewHandoff
		if name == "gateway" {
			want = errReached
		}
		if _, err := view.Executor(context.Background(), c.req, agent.ViewSession{MCP: c.mcp}); !errors.Is(err, want) {
			t.Errorf("%s: Executor = %v, want %v", name, err, want)
		}
	}
}

var errReached = errors.New("factory reached")

func validView(t *testing.T) agent.View {
	host := t.TempDir()
	return agent.View{
		Closure: []agent.ViewMount{{Name: "harness", HostDir: host}},
		Overlays: []agent.ViewOverlay{
			{Path: "/lib64/ld-linux-x86-64.so.2", Source: host, Exec: true},
			{Path: "/etc/ssl/certs", Source: host},
		},
		Masks:      []agent.ViewMask{{Path: "/etc/harness", Dir: true}},
		LocalExec:  []string{"/.oac/harness/bin/node", "/lib64/ld-linux-x86-64.so.2"},
		Shims:      []string{"bash", "git"},
		ShimPaths:  []string{"/bin/sh"},
		ForwardEnv: []string{"GIT_EDITOR"},
		Proxy:      agent.ViewProxyEnv,
		Executor: func(context.Context, proto.PromptRequestPayload, agent.ViewSession) (agent.Executor, error) {
			return nil, errors.New("not started")
		},
	}
}
