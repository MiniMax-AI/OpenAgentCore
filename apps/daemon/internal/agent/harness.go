// Package agent defines the Runtime's native Harness adapter contract.
// Start with this file when adding a Harness; the onboarding guide is at
// contracts/agents-api/harness-onboarding.md.
//
// Required lifecycle: ExecutorFactory prepares a fixed Session-owned Executor;
// each StartTurn returns a fresh Turn with its own output and settlement. Turn
// is one interface: an operation the adapter does not support returns
// ErrUnsupportedOperation before any native effects, and the capability
// declaration, not the method, decides whether the Runtime calls it. MCP,
// images, structured output and Subagent observations use protocol messages
// rather than additional Go interfaces; qualify and advertise them separately.
//
// Registration: each adapter exports one Declaration. The Runtime discovers the
// static declaration list and installs each resulting Runtime through Register.
// Availability and factory selection belong to the adapter. RegisterKind resets
// the factories, so Register installs it first. The Runtime's Environment
// owner, not the adapter, serves and declares the Environments a kind runs
// in: Register composes its EnvironmentSupport with the Harness's own
// declaration once.
//
// The Harness's support is its harnessconfig Declaration, which Core reads
// too. Discovery and the Environment owner only narrow its Capabilities, and
// the registered Executor factory runs only for a request whose selection that
// narrowed declaration admits. Requests, events and capability descriptors
// use the existing internal/agentdaemon/proto types. An Environment execution
// request carries the Runtime's bound workspace directory in
// LocalEnvironment.WorkspaceRoot; the native Harness runs there.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Declaration is the complete startup contract for a Harness implementation.
// Discover returns nil when the adapter is not configured. An unavailable
// configured adapter returns a Runtime with Available=false and no factories.
// Discovery owns runtime-specific configuration, readiness and feature gates.
type Declaration struct {
	Info          proto.SupportedAgentKind
	Configuration harnessconfig.Configuration
	Discover      func(context.Context, DiscoveryOptions, proto.SupportedAgentKind) *Runtime
}

// DiscoveryOptions provides process context without naming an implementation.
type DiscoveryOptions struct {
	Profile        string
	Stdout, Stderr io.Writer
}

// Runtime binds one discovered descriptor to its native factories.
type Runtime struct {
	Info     proto.SupportedAgentKind
	Executor ExecutorFactory
	// View declares how the Harness runs in an agent-host Session view.
	// A nil View means the agent host rejects the kind with ErrUnsupportedOperation.
	View *View
}

// EnvironmentSupport is what the Runtime's Environment owner serves. The
// Harness's own declaration states LocalEnvironment and EnvironmentNone as
// what its Executors can run; the owner decides which of them the Runtime
// offers, and serves read-only preparation and output export wherever it
// offers a local Environment.
type EnvironmentSupport struct {
	// Local serves executions in a local Environment.
	Local bool
	// None serves executions with environment none.
	None bool
}

// Compose narrows caps, a Harness's own declaration, to what s serves.
func (s EnvironmentSupport) Compose(caps proto.AgentKindCapabilities) proto.AgentKindCapabilities {
	caps.LocalEnvironment = proto.CapabilityFromBool(s.Local && caps.LocalEnvironment.IsSupported())
	caps.EnvironmentNone = proto.CapabilityFromBool(s.None && caps.EnvironmentNone.IsSupported())
	return caps
}

// Register installs a discovered Runtime with its declaration's configuration,
// in the Environments that environments serves.
func (r *Registry) Register(declaration Declaration, runtime Runtime, environments EnvironmentSupport) {
	if runtime.Info.Kind != declaration.Info.Kind {
		panic("agent.Registry.Register: discovery kind differs from declaration")
	}
	if !runtime.Info.Available && runtime.Executor != nil {
		panic("agent.Registry.Register: unavailable runtime has factories")
	}
	runtime.Info.Capabilities = environments.Compose(runtime.Info.Capabilities)
	r.RegisterKind(runtime.Info, declaration.Configuration)
	if runtime.Executor != nil {
		r.RegisterExecutor(runtime.Info.Kind, runtime.Executor)
	}
	if runtime.View != nil {
		r.RegisterView(runtime.Info.Kind, *runtime.View)
	}
}

// Agent-host Session view. The agent host runs the Harness in a per-Session
// view: the sandbox world at /, the closure, home and shims under
// ViewPrivateRoot, and a loopback-only network whose model, MCP and proxy
// endpoints belong to the Session's credential gateway. The declaration is
// data; the agent host builds each view from it and the Session. A view runs
// every request that the Runtime's declaration admits.
//
// Environment none. A request with DisableExecutionEnvironment runs in an
// empty-root view: a read-only, noexec tmpfs root that holds only the
// mountpoints for the closure, the home, the agent host's runtime files,
// ViewProcRoot, ViewDevRoot and the overlays. It has no world, no shims, no
// Link attachment and no sandbox network, so the generic proxy refuses every
// request; the cgroup, the isolation and the gateway stay. The Harness runs in
// ViewPrivateRoot/ViewHomeName/ViewWorkName. A request with neither
// LocalEnvironment nor DisableExecutionEnvironment is an incomplete binding,
// and the agent host rejects it.
//
// Environment. The Harness's environment is exactly the Env the adapter
// passes to ViewSession.Launch, which it derives from its installation and the
// request's typed fields; the request carries no environment values. A process
// in the sandbox keeps only the Harness variables that View.ForwardEnv
// declares, and the daemon's own environment reaches neither. Model and MCP
// credentials stay in the gateway's protected configuration: the agent host
// adds none to either environment or to a capability tree the view exposes.

// The view layout. This is its one definition: sessionview builds views from
// it, and View.Validate keeps declarations out of the trees it reserves.
const (
	// ViewPrivateRoot holds the closure mounts, the shim directory, the home
	// and the run directory.
	ViewPrivateRoot = "/.oac"
	// ViewShimName is the shim directory under ViewPrivateRoot.
	ViewShimName = "bin"
	// ViewHomeName is the Session home under ViewPrivateRoot.
	ViewHomeName = "home"
	// ViewRunName is the process relay's socket directory under ViewPrivateRoot.
	ViewRunName = "run"
	// ViewRelayName is the process relay's name in the shim directory, which
	// no shim takes.
	ViewRelayName = "oac-process-shim"
	// ViewWorkName is the working directory under the home in an empty-root
	// view.
	ViewWorkName = "work"
	// ViewProcRoot and ViewDevRoot are the view's own /proc and minimal /dev.
	ViewProcRoot = "/proc"
	ViewDevRoot  = "/dev"
)

// viewAliasPrefix starts every stdio MCP alias name, which no shim takes.
const viewAliasPrefix = "oac-mcp-"

// ViewAlias is the view path of the alias of the stdio binding at index i of
// ViewSession.MCP, in the shim directory.
func ViewAlias(i int) string {
	return ViewPrivateRoot + "/" + ViewShimName + "/" + viewAliasPrefix + strconv.Itoa(i)
}

// ViewReserved reports whether the view path p is at or beneath a tree the
// view builds itself: ViewPrivateRoot, ViewProcRoot or ViewDevRoot.
func ViewReserved(p string) bool {
	for _, root := range [...]string{ViewPrivateRoot, ViewProcRoot, ViewDevRoot} {
		if p == root || isWithin(p, root) {
			return true
		}
	}
	return false
}

// viewOwnedEnv are the variables the view or the process broker sets for a
// forwarded process; ForwardEnv never names them. Proxy variables match in
// any case.
var (
	viewOwnedEnv   = []string{"HOME", "PATH", "TMPDIR", "LANG", "LD_LIBRARY_PATH"}
	viewOwnedProxy = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}
)

// ErrInvalidView marks a View declaration that View.Validate rejects.
var ErrInvalidView = errors.New("agent: invalid view declaration")

// ErrViewHandoff rejects a view request that carries a model provider other
// than the Session's gateway, MCP outside ViewSession.MCP, an MCP credential
// that the gateway does not hold, or a stdio binding other than its alias.
var ErrViewHandoff = errors.New("agent: view request carries a connection outside the Session's gateway")

// ViewSession.Launch and ViewSession.Spawn outcomes.
var (
	// ErrNotLocalExec is a Launch or Spawn whose Binary is not a LocalExec path.
	ErrNotLocalExec = errors.New("agent: binary is not a LocalExec path")
	// ErrNoLiveView is a Spawn while no view runs its Harness: none was
	// launched yet, or its Harness has exited or its view has ended.
	ErrNoLiveView = errors.New("agent: the Session has no live view")
)

// View declares how the Harness runs in an agent-host Session view. View
// paths are absolute and clean. The closure, Exec overlays and the shim are
// the only executable mounts, and all are read-only; the world, the home and
// every other mount are noexec.
type View struct {
	// Closure lists the host directories presented read-only and executable
	// at ViewPrivateRoot/<Name>.
	Closure []ViewMount
	// Overlays present trusted host files or directories read-only at view paths.
	Overlays []ViewOverlay
	// Masks present view paths empty and read-only.
	Masks []ViewMask
	// LocalExec lists every view path the Harness process tree executes
	// locally. Each lies in the closure or in an Exec overlay.
	LocalExec []string
	// Shims are names on ViewPrivateRoot/ViewShimName. Each runs that name on
	// the Environment's tool PATH in the sandbox.
	Shims []string
	// ShimPaths are view paths the shim is bound over. Each runs the same
	// path in the sandbox.
	ShimPaths []string
	// ForwardEnv names the Harness variables a forwarded process keeps. It
	// never names a variable the view or the broker sets: HOME, PATH, TMPDIR,
	// LANG, LD_LIBRARY_PATH or a proxy variable. The Environment's tool
	// environment wins over a forwarded variable of the same name.
	ForwardEnv []string
	Proxy      ViewProxy
	Executor   ViewExecutorFactory
}

// ViewMount presents HostDir at ViewPrivateRoot/<Name>.
type ViewMount struct {
	Name    string
	HostDir string
}

// Path returns the mount's view path.
func (m ViewMount) Path() string { return ViewPrivateRoot + "/" + m.Name }

// ViewOverlay presents the trusted host file or directory Source at Path.
// Exec makes it executable, as the ELF interpreter of a dynamic closure
// binary must be.
type ViewOverlay struct {
	Path   string
	Source string
	Exec   bool
}

// ViewMask presents Path as an empty directory when Dir is set, otherwise as
// an empty file.
type ViewMask struct {
	Path string
	Dir  bool
}

// ViewProxy declares how the Harness reaches the network beyond its model and
// MCP endpoints. The zero value is invalid.
type ViewProxy uint8

const (
	// ViewProxyNone gives the view no generic proxy. Admission rejects a
	// request that enables a feature needing one.
	ViewProxyNone ViewProxy = iota + 1
	// ViewProxyEnv gives the view a generic proxy. The adapter has qualified
	// that every request its Harness makes locally honours HTTPS_PROXY and
	// HTTP_PROXY.
	ViewProxyEnv
)

// ViewExecutorFactory prepares the Session's Executor in its view. The agent
// host has already pointed the request's model provider at the Session's
// gateway, with the placeholder in place of the key, and moved its MCP into
// ViewSession.MCP: the request carries neither MCPHTTPServers nor
// LocalEnvironment.MCP.
type ViewExecutorFactory func(context.Context, proto.PromptRequestPayload, ViewSession) (Executor, error)

// ViewSession is what the agent host gives a view Executor factory.
type ViewSession struct {
	// Home is the per-Session native home, read-write and noexec in the view.
	// It persists across the Session's Executors.
	Home ViewDir
	// Proxy is http://127.0.0.1:<port> for ViewProxyEnv and empty for
	// ViewProxyNone.
	Proxy string
	// MCP is the Session's effective MCP, resolved once from the public
	// declarations and the installed Environment MCP. Each HTTP binding's
	// ServerURL is its loopback gateway URL, and it carries no BearerToken and
	// no HTTPHeaders; the gateway adds them. The stdio binding at index i runs
	// in the sandbox under its alias:
	// its Stdio is exactly {Server: {Name: ServerLabel, Type: "stdio",
	// Command: ViewAlias(i)}}, and the Harness runs the alias without
	// arguments. The process broker runs the binding's frozen command, args
	// and CWD for it, a relative CWD in the installation's package root, as it
	// runs a shim's process and with nothing from the Harness's argv, working
	// directory or environment. A view Executor takes MCP only from here.
	MCP []MCPBinding
	// Launch replaces clirunner.Start. Each call builds one view and runs
	// Binary, which must be a LocalExec path, in it. Dir is a world path, or
	// the work directory in an empty-root view, and Env is the complete
	// Harness environment.
	// Cancel sends TERM to every process in the view and closes the view after
	// KillTimeout; a Cancel after the Harness exited leaves its exit as it was.
	// When the Harness exits while other processes remain, the view sends them
	// TERM unless Cancel already did, and ends once they exit or KillTimeout
	// passes from the first TERM.
	Launch func(clirunner.StartOptions) (*clirunner.Process, error)
	// Spawn runs Binary, a LocalExec path, as another process in the live
	// view while its Harness runs, with StartOptions as Launch takes them. The
	// process runs as the Harness does: as the same user, in the same
	// namespaces, view cgroup, world and network, with no capabilities,
	// no_new_privs and the same seccomp filter, in a process group of its own.
	// The view starts one Spawn at a time, and Parent bounds the wait for its
	// turn and for the start; once Parent ends, Spawn returns its error and
	// kills a process that starts after all. Cancel sends TERM to the group
	// and kills it after KillTimeout; once the process has exited, Cancel
	// delivers nothing and what it left runs on as other processes in the view
	// do. The view's end ends them all: a Cancel of the Harness reaches them,
	// and when the Harness exits they are among the processes that remain. A
	// view that ends after Spawn returned shows in the process's Wait. Spawn
	// returns ErrNotLocalExec, ErrNoLiveView, or the error that kept the
	// process from starting.
	Spawn func(clirunner.StartOptions) (*clirunner.Process, error)
}

// checkViewHandoff enforces, before the factory runs, that the view request
// reaches the network only through the Session's gateway: the model provider
// is the gateway with the placeholder key, and MCP arrives only in session.MCP,
// without credentials and with stdio only under its alias.
func checkViewHandoff(req proto.PromptRequestPayload, prepared harnessconfig.PreparedConfiguration, session ViewSession) error {
	if provider := prepared.Provider; provider.APIKey != modelprovider.Placeholder || !isGatewayURL(provider.BaseURL, false) {
		return fmt.Errorf("%w: the model provider is not the Session's gateway", ErrViewHandoff)
	}
	if req.MCPHTTPServers != nil || (req.LocalEnvironment != nil && len(req.LocalEnvironment.MCP) > 0) {
		return fmt.Errorf("%w: MCP outside ViewSession.MCP", ErrViewHandoff)
	}
	for i, binding := range session.MCP {
		alias := proto.EnvironmentMCP{Server: agentplugin.MCPServer{Name: binding.ServerLabel, Type: "stdio", Command: ViewAlias(i)}}
		if binding.BearerToken != nil || len(binding.HTTPHeaders) > 0 || (binding.Transport == "http" && !isGatewayURL(binding.ServerURL, true)) ||
			(binding.Transport == "stdio" && (binding.Stdio == nil || !reflect.DeepEqual(*binding.Stdio, alias))) {
			return fmt.Errorf("%w: MCP binding %q is not a credential-free gateway endpoint or alias", ErrViewHandoff, binding.ServerLabel)
		}
	}
	return nil
}

// isGatewayURL reports whether raw is a plain HTTP URL on a loopback address
// and port, with a path only when withPath is set.
func isGatewayURL(raw string, withPath bool) bool {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.Port() == "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || (!withPath && endpoint.Path != "") {
		return false
	}
	addr, err := netip.ParseAddr(endpoint.Hostname())
	return err == nil && addr.IsLoopback()
}

// ViewDir is one directory as the adapter writes it on the host and as the
// Harness sees it in the view.
type ViewDir struct {
	Host string
	View string
}

// Validate checks the declaration without touching the host.
func (v View) Validate() error {
	if v.Executor == nil {
		return invalidView("executor is required")
	}
	if v.Proxy != ViewProxyNone && v.Proxy != ViewProxyEnv {
		return invalidView("proxy %d", v.Proxy)
	}
	names := map[string]bool{ViewShimName: true, ViewHomeName: true, ViewRunName: true}
	for _, m := range v.Closure {
		if !isPathComponent(m.Name) || names[m.Name] {
			return invalidView("closure name %q", m.Name)
		}
		names[m.Name] = true
		if !isHostPath(m.HostDir) {
			return invalidView("closure %s host directory %q", m.Name, m.HostDir)
		}
	}
	claimed := slices.Clone(v.ShimPaths)
	for _, o := range v.Overlays {
		if !isHostPath(o.Source) {
			return invalidView("overlay %s source %q", o.Path, o.Source)
		}
		claimed = append(claimed, o.Path)
	}
	for _, m := range v.Masks {
		claimed = append(claimed, m.Path)
	}
	for i, p := range claimed {
		if !isViewPath(p) || p == "/" || ViewReserved(p) {
			return invalidView("view path %q", p)
		}
		for _, q := range claimed[:i] {
			if p == q || isWithin(p, q) || isWithin(q, p) {
				return invalidView("view paths %s and %s overlap", q, p)
			}
		}
	}
	for i, p := range v.LocalExec {
		if !isViewPath(p) || slices.Contains(v.LocalExec[:i], p) || !v.executable(p) {
			return invalidView("local exec %q is not a unique path in the closure or an Exec overlay", p)
		}
	}
	for i, n := range v.Shims {
		if !isPathComponent(n) || n == ViewRelayName || strings.HasPrefix(n, viewAliasPrefix) || slices.Contains(v.Shims[:i], n) {
			return invalidView("shim %q", n)
		}
	}
	for i, n := range v.ForwardEnv {
		if n == "" || strings.ContainsAny(n, "=\x00") || slices.Contains(v.ForwardEnv[:i], n) || viewOwnsEnv(n) {
			return invalidView("forwarded variable %q", n)
		}
	}
	return nil
}

func (v View) executable(p string) bool {
	for _, m := range v.Closure {
		if isWithin(p, m.Path()) {
			return true
		}
	}
	for _, o := range v.Overlays {
		if o.Exec && (p == o.Path || isWithin(p, o.Path)) {
			return true
		}
	}
	return false
}

func (v View) clone() View {
	v.Closure = slices.Clone(v.Closure)
	v.Overlays = slices.Clone(v.Overlays)
	v.Masks = slices.Clone(v.Masks)
	v.LocalExec = slices.Clone(v.LocalExec)
	v.Shims = slices.Clone(v.Shims)
	v.ShimPaths = slices.Clone(v.ShimPaths)
	v.ForwardEnv = slices.Clone(v.ForwardEnv)
	return v
}

func viewOwnsEnv(name string) bool {
	return slices.Contains(viewOwnedEnv, name) || slices.ContainsFunc(viewOwnedProxy, func(proxy string) bool { return strings.EqualFold(name, proxy) })
}

func invalidView(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidView, fmt.Sprintf(format, args...))
}

func isViewPath(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsRune(p, 0)
}

func isHostPath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsRune(p, 0)
}

func isPathComponent(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

// isWithin reports whether p is strictly beneath dir.
func isWithin(p, dir string) bool {
	return strings.HasPrefix(p, dir+"/")
}

// RegisterView validates and installs the kind's agent-host view after
// RegisterKind. Its Executor factory validates the model configuration like
// RegisterExecutor and then checks the request against the view's gateway rule.
func (r *Registry) RegisterView(kind string, view View) {
	if err := view.Validate(); err != nil {
		panic(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	configuration, declared := r.configurations[kind]
	if !declared {
		panic("agent.Registry.RegisterView: registered kind required")
	}
	view = view.clone()
	factory := view.Executor
	view.Executor = func(ctx context.Context, req proto.PromptRequestPayload, session ViewSession) (Executor, error) {
		prepared, err := configuration.Prepare(req)
		if err != nil {
			return nil, err
		}
		if err := checkViewHandoff(req, prepared, session); err != nil {
			return nil, err
		}
		return factory(ctx, req, session)
	}
	r.views[kind] = view
}

// ResolveView returns the kind's agent-host view. A kind registered without
// one wraps ErrUnsupportedOperation.
func (r *Registry) ResolveView(kind string) (View, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.kinds[kind]; !ok {
		return View{}, fmt.Errorf("%w: %q", ErrUnsupportedKind, kind)
	}
	view, ok := r.views[kind]
	if !ok {
		return View{}, fmt.Errorf("%w: %q declares no agent-host view", ErrUnsupportedOperation, kind)
	}
	return view.clone(), nil
}

// Model configuration and support have one shared contract, authored in
// internal/harnessconfig/harness.go. RegisterKind requires that declaration;
// RegisterExecutor inherits it. Every registered entry validates the selection,
// model, provider and native parameters before calling native code. The
// declaration belongs to the adapter and is also consumed by Core. Keep
// adapter field rules and rendering private. That shared contract owns frozen
// configuration and native application obligations. This file owns execution
// lifecycle only.
//
// Preparation failure retains unconfirmed native cleanup in a non-nil Executor
// under the factory ownership contract below.

// Required execution lifecycle.

// ExecutorFactory prepares without model input. A failed factory retains any
// unconfirmed cleanup in its non-nil Executor.
type ExecutorFactory func(context.Context, proto.PromptRequestPayload) (Executor, error)

// Executor retains a fixed native configuration across independently owned Turns.
type Executor interface {
	// A nil Turn guarantees no input was submitted or output retained and leaves
	// out with the caller. A non-nil Turn owns out, including on error; input
	// may have been submitted and must never be replayed automatically.
	StartTurn(context.Context, string, proto.MessageInput, chan<- proto.Envelope) (Turn, error)
	// Success confirms all native work and owned output streams have stopped.
	// It does not establish an otherwise unknown Turn result. An error retains
	// resource ownership; another caller may await or retry Close.
	Close(context.Context) error
}

// Turn owns one output stream and never retargets cancellation to a successor.
// For Executor-owned Turns, AwaitSettlement and Executor.Close define settlement
// and resource retirement; Cancel alone does not transfer resource ownership.
type Turn interface {
	// Cancel signals the Turn to abort. Idempotent. Actual teardown
	// happens asynchronously and is signalled via the out channel close.
	Cancel(ctx context.Context) error
	// CancellationOutcome snapshots observed native identity, usage and output.
	// It remains readable after Cancel; missing evidence stays unset. An empty
	// result means no observed evidence, not unsupported cancellation or success.
	// Reading it does not wait for or establish native settlement.
	CancellationOutcome() proto.DonePayload
	// SteerWithReceipt delivers active input: it calls written once the complete
	// input is written, then waits for the native application receipt. Every
	// public Harness implements it; returning Unsupported is not a receipt.
	SteerWithReceipt(ctx context.Context, input proto.PromptSteerPayload, written func()) error
	// SubmitFunctionResult delivers a result for an outstanding native call.
	// A successful return requires its native application receipt.
	SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error
	// Success confirms closed output and settled native input, function and
	// child-work obligations. Errors cannot prove cancellation.
	AwaitSettlement(context.Context) (TurnSettlement, error)
}

// TurnSettlement describes native readiness after this Turn has fully drained.
// A false result must carry a reason; it requires confirmed Executor.Close.
type TurnSettlement struct {
	Reusable bool
	Reason   string
}

// Kind registration.

// RegisterKind installs the heartbeat descriptor and model configuration for an
// agent_kind. Callers may set Available=false when an adapter exists but its
// underlying CLI is not usable.
func (r *Registry) RegisterKind(info proto.SupportedAgentKind, configuration harnessconfig.Configuration) {
	kind := info.Kind
	if kind == "" {
		panic("agent.Registry.Register: empty kind")
	}
	if err := configuration.ValidateDeclaration(); err != nil {
		panic(err)
	}
	if err := info.ValidateDeclaration(); err != nil {
		panic(err)
	}
	configuration = configuration.Clone()
	declaration, err := configuration.Declaration.Narrow(info.Capabilities)
	if err != nil {
		panic(err)
	}
	configuration.Declaration = declaration
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configurations[kind] = configuration
	delete(r.executors, kind)
	delete(r.views, kind)
	r.kinds[kind] = info
}

// RegisterExecutor installs the shared lifecycle after RegisterKind. It does
// not enable other public operations.
func (r *Registry) RegisterExecutor(kind string, factory ExecutorFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.kinds[kind]
	if !exists || factory == nil {
		panic("agent.Registry.RegisterExecutor: registered kind and factory required")
	}
	configuration, declared := r.configurations[kind]
	if !declared {
		panic("agent.Registry.RegisterExecutor: configuration required")
	}
	r.executors[kind] = func(ctx context.Context, req proto.PromptRequestPayload) (Executor, error) {
		if _, err := configuration.Prepare(req); err != nil {
			return nil, err
		}
		return factory(ctx, req)
	}
}
