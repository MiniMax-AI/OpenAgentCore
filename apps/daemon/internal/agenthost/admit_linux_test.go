//go:build linux

package agenthost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processbroker"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
)

var errFactory = errors.New("factory reached")

// viewFixture registers "viewed", whose factory records what it receives and
// whose view supports no capability, "supporting", whose view supports every
// capability, "masked", whose view masks an /etc file the agent host writes,
// "shimmed", whose view runs a shim name on the sandbox PATH, and "plain",
// which declares no view.
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
		Closure:      []agent.ViewMount{{Name: "harness", HostDir: t.TempDir()}},
		LocalExec:    []string{"/.oac/harness/harness"},
		Proxy:        agent.ViewProxyEnv,
		Capabilities: declared(proto.CapabilityUnsupported),
		Executor: func(_ context.Context, req proto.PromptRequestPayload, s agent.ViewSession) (agent.Executor, error) {
			f.req, f.session = req, s
			info, err := os.Stat(s.Home.Host)
			f.homeSet = err == nil && info.IsDir()
			return nil, errFactory
		},
	}
	register(reg, "viewed", &view)
	supporting := view
	supporting.Capabilities = declared(proto.CapabilitySupported)
	register(reg, "supporting", &supporting)
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
	unsupported := []error{ErrUnsupported, agent.ErrUnsupportedOperation}
	for name, c := range map[string]struct {
		kind   string
		change func(*proto.PromptRequestPayload)
		want   []error
	}{
		"kind without a view":                {"plain", func(*proto.PromptRequestPayload) {}, unsupported},
		"view meeting the agent host's /etc": {"masked", func(*proto.PromptRequestPayload) {}, []error{ErrUnsupported, agent.ErrInvalidView}},
		"incomplete binding":                 {"supporting", func(r *proto.PromptRequestPayload) { r.LocalEnvironment = nil }, []error{ErrInvalidSession}},
		"shim name without PATH":             {"shimmed", func(*proto.PromptRequestPayload) {}, []error{ErrInvalidSession}},
		"relative workspace":                 {"viewed", func(r *proto.PromptRequestPayload) { r.LocalEnvironment.WorkspaceDirectory = "workspace" }, []error{ErrInvalidSession}},
		"no model provider":                  {"viewed", func(r *proto.PromptRequestPayload) { r.ModelProvider = nil }, []error{ErrUnsupported}},
		// The typed rejections that hold whatever the view declares.
		"no strict resume":        {"supporting", func(r *proto.PromptRequestPayload) { r.StrictResume = false }, unsupported},
		"restricted network":      {"supporting", func(r *proto.PromptRequestPayload) { r.LocalEnvironment.NetworkAccess = "disabled" }, unsupported},
		"allowed domains only":    {"supporting", func(r *proto.PromptRequestPayload) { r.LocalEnvironment.AllowedDomains = []string{"example.com"} }, unsupported},
		"unprepared Capabilities": {"supporting", func(r *proto.PromptRequestPayload) { r.LocalEnvironment.Capabilities = true }, unsupported},
		"credentialed stdio MCP": {"supporting", func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment.MCP = []proto.EnvironmentMCP{{Server: agentplugin.MCPServer{Name: "tools", Type: "stdio", Command: "tools", EnvVars: []string{"TOKEN"}}}}
		}, []error{ErrUnsupported, agent.ErrViewHandoff}},
		"stdio MCP without an absolute directory": {"supporting", func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment.MCP = []proto.EnvironmentMCP{{PackageRoot: "pkg", Server: agentplugin.MCPServer{Name: "tools", Type: "stdio", Command: "/bin/tools"}}}
		}, []error{ErrInvalidSession}},
		"stdio MCP name without PATH": {"supporting", func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment.MCP = []proto.EnvironmentMCP{{InstallationRoot: "/capabilities", Server: agentplugin.MCPServer{Name: "tools", Type: "stdio", Command: "tools"}}}
		}, []error{ErrInvalidSession}},
	} {
		req := request(c.kind, "/workspace", "https://model.test", "sk-test")
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

func TestAdmissionFollowsDeclarations(t *testing.T) {
	f := newViewFixture(t)
	roots, err := checkConfig(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	network := func(context.Context) (sandboxlink.Stream, error) { return nil, errors.New("not dialled") }
	for name, c := range map[string]struct {
		use                func(*proto.PromptRequestPayload)
		viewed, supporting bool
	}{
		"environment none": {func(r *proto.PromptRequestPayload) { r.DisableExecutionEnvironment, r.LocalEnvironment = true, nil }, false, true},
		"Skills": {func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment.Capabilities, r.LocalEnvironment.CapabilityRoot = true, "/capabilities"
			r.LocalEnvironment.Skills = []agentcapabilities.InstalledSkill{{RelativeRoot: "skills/review"}}
		}, false, true},
		"function tools": {func(r *proto.PromptRequestPayload) { r.FunctionTools = []proto.FunctionTool{{Name: "lookup"}} }, false, true},
		"tool search":    {func(r *proto.PromptRequestPayload) { r.ToolSearch = true }, false, true},
		"stdio MCP": {func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment.MCP = []proto.EnvironmentMCP{{InstallationRoot: "/capabilities", Server: agentplugin.MCPServer{Name: "tools", Type: "stdio", Command: "/bin/tools"}}}
		}, false, true},
		// An installation with only HTTP MCP needs no Skill support.
		"installed HTTP MCP": {func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment.Capabilities, r.LocalEnvironment.CapabilityRoot = true, "/capabilities"
			r.LocalEnvironment.MCP = []proto.EnvironmentMCP{{Server: agentplugin.MCPServer{Name: "docs", Type: "http", URL: "https://mcp.test/docs"}}}
		}, true, true},
	} {
		for kind, admitted := range map[string]bool{"viewed": c.viewed, "supporting": c.supporting} {
			req := request(kind, "/workspace", "https://model.test", "sk-test")
			c.use(&req)
			if _, err := admit(f.cfg, roots, req, Environment{}, network); admitted != (err == nil) || (err != nil && !errors.Is(err, ErrUnsupported)) {
				t.Errorf("%s on %s: admit = %v, want admitted %v or ErrUnsupported", name, kind, err, admitted)
			}
		}
	}
}

// The binding at index i runs under alias i, which the process broker maps to
// the frozen command; only HTTP bindings reach the gateway.
func TestStdioMCPRunsUnderItsAlias(t *testing.T) {
	f := newViewFixture(t)
	roots, err := checkConfig(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := request("supporting", "/workspace", "https://model.test", "sk-test")
	req.LocalEnvironment.MCP = []proto.EnvironmentMCP{
		{Server: agentplugin.MCPServer{Name: "docs", Type: "http", URL: "https://mcp.test/docs"}},
		{InstallationRoot: "/capabilities", PackageRoot: "pkg", Server: agentplugin.MCPServer{Name: "tools", Type: "stdio", Command: "bin/tools", Args: []string{"--stdio"}, CWD: "run"}},
	}
	network := func(context.Context) (sandboxlink.Stream, error) { return nil, errors.New("not dialled") }
	p, err := admit(f.cfg, roots, req, Environment{}, network)
	if err != nil {
		t.Fatal(err)
	}
	alias := proto.EnvironmentMCP{Server: agentplugin.MCPServer{Name: "tools", Type: "stdio", Command: agent.ViewAlias(1)}}
	if len(p.mcp) != 2 || p.mcp[1].Stdio == nil || !reflect.DeepEqual(*p.mcp[1].Stdio, alias) || len(p.gateway.MCP) != 1 || p.gateway.MCP[0].ServerLabel != "docs" {
		t.Errorf("ViewSession.MCP %+v and gateway MCP %+v; want the stdio binding under its alias and only HTTP at the gateway", p.mcp, p.gateway.MCP)
	}
	want := map[string]processbroker.Command{"oac-mcp-1": {Executable: "bin/tools", Args: []string{"--stdio"}, Dir: "/capabilities/pkg/run"}}
	if !reflect.DeepEqual(p.executables.Aliases, want) {
		t.Errorf("aliases %+v, want %+v", p.executables.Aliases, want)
	}
}

func TestRegistryDescribesTheViewPath(t *testing.T) {
	f := newViewFixture(t)
	var kinds []string
	for _, info := range (&Host{cfg: f.cfg}).Registry(nil).SupportedAgentKinds() {
		kinds = append(kinds, info.Kind)
		c, declared := info.Capabilities, info.Kind == "supporting"
		if !c.Preparation.IsSupported() || !c.LocalEnvironment.IsSupported() || !c.MCPHTTPTools.IsSupported() || c.EnvironmentNone.IsSupported() != declared ||
			c.FunctionTools.IsSupported() != declared || c.FunctionResultImages.IsSupported() != declared || c.ToolSearch.IsSupported() != declared ||
			c.WorkspaceOutputExport.IsSupported() || c.WorkspaceReadPreparation.IsSupported() {
			t.Errorf("%s: capabilities %+v do not describe the view path", info.Kind, c)
		}
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{"masked", "shimmed", "supporting", "viewed"}) {
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
