//go:build linux

package agenthost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
)

var errFactory = errors.New("factory reached")

// viewFixture registers "viewed", whose factory records what it receives,
// "masked", whose view masks an /etc file the agent host writes, "shimmed",
// whose view runs a shim name on the sandbox PATH, and "plain", which
// declares no view.
type viewFixture struct {
	cfg     Config
	req     proto.PromptRequestPayload
	session agent.ViewSession
	homeSet bool
}

func newViewFixture(t *testing.T) *viewFixture {
	upstream := httptest.NewTLSServer(http.NotFoundHandler())
	upstream.Close()
	f := &viewFixture{}
	reg := agent.NewRegistry()
	view := agent.View{
		Closure:   []agent.ViewMount{{Name: "harness", HostDir: t.TempDir()}},
		LocalExec: []string{"/.oac/harness/harness"},
		Proxy:     agent.ViewProxyEnv,
		Executor: func(_ context.Context, req proto.PromptRequestPayload, s agent.ViewSession) (agent.Executor, error) {
			f.req, f.session = req, s
			info, err := os.Stat(s.Home.Host)
			f.homeSet = err == nil && info.IsDir()
			return nil, errFactory
		},
	}
	register(reg, "viewed", &view)
	masked := view
	masked.Masks = []agent.ViewMask{{Path: "/etc/passwd"}}
	register(reg, "masked", &masked)
	shimmed := view
	shimmed.Shims = []string{"git"}
	register(reg, "shimmed", &shimmed)
	register(reg, "plain", nil)
	f.cfg = newConfig(t, reg, upstream.Certificate())
	return f
}

func TestAdmissionRejectsBeforeAnyEffect(t *testing.T) {
	f := newViewFixture(t)
	for name, c := range map[string]struct {
		change func(*proto.PromptRequestPayload)
		want   []error
	}{
		"kind without a view":                {func(r *proto.PromptRequestPayload) { r.AgentKind = "plain" }, []error{ErrUnsupported, agent.ErrUnsupportedOperation}},
		"view meeting the agent host's /etc": {func(r *proto.PromptRequestPayload) { r.AgentKind = "masked" }, []error{ErrUnsupported, agent.ErrInvalidView}},
		"environment none": {func(r *proto.PromptRequestPayload) {
			r.DisableExecutionEnvironment, r.LocalEnvironment = true, nil
		}, []error{ErrUnsupported, agent.ErrUnsupportedOperation}},
		"shim name without PATH": {func(r *proto.PromptRequestPayload) { r.AgentKind = "shimmed" }, []error{ErrInvalidSession}},
		"relative workspace":     {func(r *proto.PromptRequestPayload) { r.LocalEnvironment.WorkspaceDirectory = "workspace" }, []error{ErrInvalidSession}},
		"no model provider":      {func(r *proto.PromptRequestPayload) { r.ModelProvider = nil }, []error{ErrUnsupported}},
		"no strict resume":       {func(r *proto.PromptRequestPayload) { r.StrictResume = false }, []error{ErrUnsupported}},
		"capabilities":           {func(r *proto.PromptRequestPayload) { r.LocalEnvironment.Capabilities = true }, []error{ErrUnsupported}},
		"restricted network":     {func(r *proto.PromptRequestPayload) { r.LocalEnvironment.NetworkAccess = "disabled" }, []error{ErrUnsupported}},
		"allowed domains only":   {func(r *proto.PromptRequestPayload) { r.LocalEnvironment.AllowedDomains = []string{"example.com"} }, []error{ErrUnsupported}},
		"function tools":         {func(r *proto.PromptRequestPayload) { r.FunctionTools = []proto.FunctionTool{{Name: "lookup"}} }, []error{ErrUnsupported, agent.ErrUnsupportedOperation}},
		"stdio MCP": {func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment.MCP = []proto.EnvironmentMCP{{Server: agentplugin.MCPServer{Name: "tools", Type: "stdio", Command: "tools"}}}
		}, []error{ErrUnsupported, agent.ErrUnsupportedOperation}},
	} {
		req := request("viewed", "/workspace", "https://model.test", "sk-test")
		c.change(&req)
		var dials atomic.Int32
		e, err := open(context.Background(), f.cfg, req, bindTo(newBinding(newResource())), deps{dial: countingDial(&dials), tasks: noTasks})
		for _, want := range c.want {
			if !errors.Is(err, want) {
				t.Errorf("%s: open = %v, want %v", name, err, want)
			}
		}
		if e != nil || dials.Load() != 0 {
			t.Errorf("%s: an Executor after %d dials", name, dials.Load())
		}
		if _, err := os.Stat(sessionsDir(f.cfg.StateDir)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: the sessions directory exists", name)
		}
	}
	// A binding that Link encoding refuses is refused before any effect.
	for name, change := range map[string]func(*Binding){
		"zero assignment epoch":  func(b *Binding) { b.AssignmentEpoch = 0 },
		"invalid resource kind":  func(b *Binding) { b.Resource.Kind = 0 },
		"oversized attach grant": func(b *Binding) { b.AttachGrant = make([]byte, sandboxlink.MaxGrantBytes+1) },
	} {
		b := newBinding(newResource())
		change(&b)
		var dials atomic.Int32
		e, err := open(context.Background(), f.cfg, request("viewed", "/workspace", "https://model.test", "sk-test"), bindTo(b), deps{dial: countingDial(&dials), tasks: noTasks})
		if e != nil || !errors.Is(err, ErrInvalidSession) || dials.Load() != 0 {
			t.Errorf("%s: open = %v after %d dials, want ErrInvalidSession", name, err, dials.Load())
		}
		if _, err := os.Stat(sessionsDir(f.cfg.StateDir)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: the sessions directory exists", name)
		}
	}
}

func TestRegistryDescribesTheViewPath(t *testing.T) {
	f := newViewFixture(t)
	var kinds []string
	for _, info := range (&Host{cfg: f.cfg}).Registry(nil).SupportedAgentKinds() {
		kinds = append(kinds, info.Kind)
		c := info.Capabilities
		if !c.Preparation.IsSupported() || !c.LocalEnvironment.IsSupported() || !c.MCPHTTPTools.IsSupported() || c.EnvironmentNone.IsSupported() ||
			c.FunctionTools.IsSupported() || c.WorkspaceOutputExport.IsSupported() || c.WorkspaceReadPreparation.IsSupported() {
			t.Errorf("%s: capabilities %+v do not describe the view path", info.Kind, c)
		}
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{"masked", "shimmed", "viewed"}) {
		t.Errorf("kinds %v, want those that declare a view", kinds)
	}
}

func TestViewExecutorReceivesTheGatewayRequest(t *testing.T) {
	f := newViewFixture(t)
	bearer := "mcp-secret"
	req := request("viewed", "/workspace", "https://model.test", "sk-test")
	req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "environment", ServerLabel: "docs", ServerURL: "https://mcp.test/docs?tenant=a", BearerToken: &bearer}}
	original := *req.ModelProvider
	var dials atomic.Int32
	d := newDaemon(t, f.cfg, deps{dial: countingDial(&dials), tasks: noTasks})
	if _, p := d.prepare(t, newBinding(newResource()), req); p.State != "failed" || p.ErrorCode != "preparation_failed" {
		t.Fatalf("the preparation is %s (%s), want failed with the factory", p.State, p.ErrorCode)
	}
	if provider := f.req.ModelProvider; provider == nil || provider.BaseURL != "http://127.0.0.1:17101" || provider.APIKey != modelprovider.Placeholder || provider.Protocol != modelprovider.Anthropic {
		t.Errorf("model provider %+v; want the gateway with the placeholder", provider)
	}
	if *req.ModelProvider != original {
		t.Error("the Session's request changed")
	}
	if f.req.MCPHTTPServers != nil || f.req.LocalEnvironment == nil || f.req.LocalEnvironment.MCP != nil || f.req.LocalEnvironment.WorkspaceRoot != "/workspace" {
		t.Error("the request still carries MCP or does not run in the Environment's workspace")
	}
	mcp := f.session.MCP
	if len(mcp) != 1 || mcp[0].ServerLabel != "docs" || mcp[0].ServerURL != "http://127.0.0.1:17102/docs" || mcp[0].BearerToken != nil || mcp[0].HTTPHeaders != nil {
		t.Errorf("ViewSession.MCP = %+v, want one credential-free gateway binding", mcp)
	}
	if f.session.Proxy != "http://127.0.0.1:17100" || f.session.Home.View != "/.oac/home" || !f.homeSet || f.session.Launch == nil {
		t.Errorf("ViewSession proxy %q, home %+v (present %v)", f.session.Proxy, f.session.Home, f.homeSet)
	}
	if !strings.HasPrefix(f.session.Home.Host, sessionsDir(f.cfg.StateDir)+string(filepath.Separator)) {
		t.Errorf("home %s is outside the Session directories", f.session.Home.Host)
	}
	// The failed preparation closed its Executor, which keeps only the home.
	if _, err := os.Stat(f.session.Home.Host); dials.Load() != 0 || err != nil || len(leftEntries(t, f.cfg)) != 0 {
		t.Errorf("%d dials, home %v and transient entries %v after the preparation", dials.Load(), err, leftEntries(t, f.cfg))
	}

}
