package agent_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
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
		"unclean view path":           func(v *agent.View) { v.Masks[0].Path = "/etc/../etc/harness" },
		"duplicate shim":              func(v *agent.View) { v.Shims = append(v.Shims, "git") },
		"forwarded assignment":        func(v *agent.View) { v.ForwardEnv = append(v.ForwardEnv, "A=B") },
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
