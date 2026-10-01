package agenthost

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processbroker"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// modelName is the gateway's name for the request's model provider.
const modelName = "model_provider"

// etcFiles are the files the agent host writes for each Session and presents
// at /etc/<name>.
var etcFiles = []string{"passwd", "group", "hosts", "resolv.conf", "nsswitch.conf"}

// plan is an admitted Session: everything Run derives before any effect.
type plan struct {
	view    agent.View
	gateway gateway.Config
	// request is the request the view Executor factory receives.
	request proto.PromptRequestPayload
	// mcp and proxy are ViewSession.MCP and ViewSession.Proxy.
	mcp   []agent.MCPBinding
	proxy string
	// executables is each view's process broker table: each shim name runs
	// that name on the sandbox PATH, and each shim path the same path.
	executables processbroker.Executables
}

// checkConfig validates cfg and loads the roots in its CA directory.
func checkConfig(cfg Config) (*x509.CertPool, error) {
	switch {
	case !isHostPath(cfg.StateDir):
		return nil, invalidConfig("state directory %q is not absolute and clean", cfg.StateDir)
	case !cfg.UIDs.valid():
		return nil, invalidConfig("uid range %d+%d", cfg.UIDs.First, cfg.UIDs.Count)
	case sandboxlink.CheckRelayURL(cfg.RelayURL) != nil:
		return nil, invalidConfig("relay URL")
	case cfg.RuntimeID.IsZero() || len(cfg.Credential) == 0:
		return nil, invalidConfig("no Runtime ID or credential")
	case cfg.Harnesses == nil:
		return nil, invalidConfig("no Harness declarations")
	case !isHostPath(cfg.Shim):
		return nil, invalidConfig("shim %q is not absolute and clean", cfg.Shim)
	case !isHostPath(cfg.CADir) || cfg.CADir == "/" || agent.ViewReserved(cfg.CADir):
		return nil, invalidConfig("CA directory %q", cfg.CADir)
	}
	return loadRoots(cfg.CADir)
}

// loadRoots reads every certificate in dir. Each entry is a regular file of
// PEM certificates, so the view presents exactly what the gateway trusts.
func loadRoots(dir string) (*x509.CertPool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, &Error{Kind: ErrInvalidConfig, Op: "CA directory", Err: err}
	}
	if len(entries) == 0 {
		return nil, invalidConfig("CA directory %s is empty", dir)
	}
	roots := x509.NewCertPool()
	for _, e := range entries {
		if !e.Type().IsRegular() {
			return nil, invalidConfig("CA entry %s is not a regular file", e.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, &Error{Kind: ErrInvalidConfig, Op: "CA directory", Err: err}
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, invalidConfig("CA entry %s holds no PEM certificate", e.Name())
		}
	}
	return roots, nil
}

// admit checks the Session and derives its plan without touching anything.
// openNetwork is the Session's Network dial for the gateway.
func admit(cfg Config, roots *x509.CertPool, s Session, openNetwork func(context.Context) (sandboxlink.Stream, error)) (*plan, error) {
	if err := checkSession(s); err != nil {
		return nil, err
	}
	req := s.Request
	view, err := cfg.Harnesses.ResolveView(req.AgentKind)
	if err != nil {
		return nil, &Error{Kind: ErrUnsupported, Op: "admit", Err: err}
	}
	local := req.LocalEnvironment
	switch {
	case req.DisableExecutionEnvironment:
		return nil, unsupported("a Session without an execution environment")
	case local == nil:
		return nil, unsupported("a Session without a workspace")
	case !isViewPath(local.WorkspaceRoot):
		return nil, invalidSession("workspace %q is not absolute and clean", local.WorkspaceRoot)
	case !req.StrictResume:
		return nil, unsupported("a Session without strict resume")
	case local.Capabilities || len(local.Skills) > 0 || local.CapabilityRoot != "":
		return nil, unsupported("installed Capabilities and skills")
	case local.NetworkAccess != "enabled" || len(local.AllowedDomains) > 0:
		return nil, unsupported("a restricted workspace network")
	case len(req.FunctionTools) > 0 || req.ToolSearch:
		// A function call waits for a result that Input cannot deliver.
		return nil, unsupported("function tools and their discovery")
	case len(view.Shims) > 0 && !hasPATH(s.Environment):
		return nil, invalidSession("the view's shims run names on the sandbox PATH, and the Environment sets no PATH")
	}
	raw, ok := req.AgentOptions["model_provider"]
	if !ok {
		return nil, unsupported("a Session without a frozen model provider")
	}
	provider, err := modelprovider.ParseProvider(raw)
	if err != nil {
		return nil, invalidSession("model provider: %v", err)
	}
	bindings, err := agent.ResolveMCPBindings(req)
	if err != nil {
		return nil, invalidSession("MCP: %v", err)
	}
	for _, b := range bindings {
		if b.Transport != "http" {
			return nil, unsupported("%s MCP server %q", b.Transport, b.ServerLabel)
		}
	}
	if err := checkLayout(cfg, view); err != nil {
		return nil, err
	}
	gw := gateway.Config{
		Models:      []gateway.Model{{Name: modelName, Provider: provider}},
		MCP:         bindings,
		Prompt:      req,
		OpenNetwork: openNetwork,
		RootCAs:     roots,
		Proxy:       view.Proxy == agent.ViewProxyEnv,
	}
	endpoints, err := gateway.Plan(gw)
	if err != nil {
		return nil, &Error{Kind: ErrInvalidSession, Op: "gateway", Err: err}
	}
	p := &plan{view: view, gateway: gw, proxy: endpoints.Proxy,
		executables: processbroker.Executables{Names: identity(view.Shims), Paths: identity(view.ShimPaths)}}
	if p.request, err = handoff(req, provider, endpoints); err != nil {
		return nil, err
	}
	for _, b := range bindings {
		b.ServerURL, b.BearerToken, b.HTTPHeaders = endpoints.MCP[b.ServerLabel], nil, nil
		if b.AllowedTools != nil {
			tools := slices.Clone(*b.AllowedTools)
			b.AllowedTools = &tools
		}
		p.mcp = append(p.mcp, b)
	}
	return p, nil
}

// handoff rewrites the request as a view Executor receives it: the model
// provider is the gateway's listener with the placeholder key, and MCP is
// only in ViewSession.MCP.
func handoff(req proto.PromptRequestPayload, provider modelprovider.Provider, endpoints gateway.Endpoints) (proto.PromptRequestPayload, error) {
	provider.BaseURL, provider.APIKey = endpoints.Models[modelName], modelprovider.Placeholder
	encoded, err := json.Marshal(provider)
	if err != nil {
		return req, invalidSession("model provider: %v", err)
	}
	var option map[string]any
	if err := json.Unmarshal(encoded, &option); err != nil {
		return req, invalidSession("model provider: %v", err)
	}
	req.AgentOptions = maps.Clone(req.AgentOptions)
	req.AgentOptions["model_provider"] = option
	req.MCPHTTPServers = nil
	local := *req.LocalEnvironment
	local.MCP = nil
	req.LocalEnvironment = &local
	return req, nil
}

// checkLayout rejects a view whose overlays, masks or shim paths meet the
// agent host's own overlays: the /etc files and the CA directory.
func checkLayout(cfg Config, view agent.View) error {
	own := []string{cfg.CADir}
	for _, name := range etcFiles {
		own = append(own, "/etc/"+name)
	}
	claimed := slices.Clone(view.ShimPaths)
	for _, o := range view.Overlays {
		claimed = append(claimed, o.Path)
	}
	for _, m := range view.Masks {
		claimed = append(claimed, m.Path)
	}
	for _, p := range claimed {
		for _, q := range own {
			if p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
				return &Error{Kind: ErrUnsupported, Op: "admit", Err: fmt.Errorf("%w: view path %s meets the agent host's %s", agent.ErrInvalidView, p, q)}
			}
		}
	}
	return nil
}

// checkSession checks the Session's own fields. Its binding is valid when the
// Open the Session sends is, as Link encoding checks it.
func checkSession(s Session) error {
	if _, err := sandboxlink.Encode(1, s.Binding.open(sandboxlink.ServiceFile, sandboxfs.Version, sandboxwire.ID{})); err != nil {
		return invalidSession("binding: %v", err)
	}
	if s.Input == nil || s.Output == nil {
		return invalidSession("no input or output channel")
	}
	for _, env := range []map[string]string{s.Environment.Sandbox, s.Environment.Tool} {
		for name, value := range env {
			if name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, 0) {
				return invalidSession("environment variable %q", name)
			}
		}
	}
	return nil
}

// hasPATH reports whether a forwarded process receives PATH.
func hasPATH(env Environment) bool {
	_, sandbox := env.Sandbox["PATH"]
	_, tool := env.Tool["PATH"]
	return sandbox || tool
}

// identity maps each of keys to itself.
func identity(keys []string) map[string]string {
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		m[k] = k
	}
	return m
}

// valid reports whether r is a nonempty range of nonzero uids.
func (r UIDRange) valid() bool {
	return r.First != 0 && r.Count != 0 && uint64(r.First)+uint64(r.Count) <= math.MaxUint32
}

func isHostPath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsRune(p, 0)
}

func isViewPath(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsRune(p, 0)
}

func unsupported(format string, args ...any) error {
	return &Error{Kind: ErrUnsupported, Op: "admit", Err: fmt.Errorf("%w: %s", agent.ErrUnsupportedOperation, fmt.Sprintf(format, args...))}
}

func invalidSession(format string, args ...any) error {
	return &Error{Kind: ErrInvalidSession, Op: "admit", Err: fmt.Errorf(format, args...)}
}

func invalidConfig(format string, args ...any) error {
	return &Error{Kind: ErrInvalidConfig, Err: fmt.Errorf(format, args...)}
}
