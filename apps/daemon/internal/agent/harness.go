// Package agent defines the Runtime's native Harness adapter contract.
// Start with this file when adding a Harness; the onboarding guide is at
// contracts/agents-api/harness-onboarding.md.
//
// Required lifecycle: ExecutorFactory prepares a fixed Session-owned Executor;
// each StartTurn returns a fresh Turn with its own output and settlement.
// Turn includes the public text DurableSteerer requirement. Keep extension
// interfaces separate, but implement each explicitly: unsupported operations
// return ErrUnsupportedOperation before any native effects. Interface presence
// does not advertise support; the capability declaration controls admission. MCP,
// images, structured output and Subagent observations use protocol messages
// rather than additional Go interfaces; qualify and advertise them separately.
//
// Registration: each adapter exports one Declaration. The Runtime discovers the
// static declaration list and installs each resulting Runtime through Register.
// Availability and factory selection belong to the adapter. RegisterKind resets
// the other factories, so Register installs it first. Preparation capabilities
// are derived from the declared factories.
//
// Runtime registration and Core service qualification remain separate. A public
// Harness also needs a profile in services/core/internal/engine; advertising
// a capability cannot authorize it. Requests, events and capability descriptors
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
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Declaration is the complete startup contract for a Harness implementation.
// Discover returns nil when the adapter is not configured. An unavailable
// configured adapter returns a Runtime with Available=false and a session factory.
// Discovery owns runtime-specific configuration, readiness and feature gates.
type Declaration struct {
	Info          proto.SupportedAgentKind
	Configuration harnessconfig.Configuration
	// ConnectionOptions lists the AgentOptions keys whose values carry MCP
	// servers, endpoints, credentials or environment values. An agent-host
	// view rejects a request that sets any of them with ErrViewHandoff.
	ConnectionOptions []string
	Discover          func(context.Context, DiscoveryOptions, proto.SupportedAgentKind) *Runtime
}

// DiscoveryOptions provides process context without naming an implementation.
type DiscoveryOptions struct {
	Profile        string
	Stdout, Stderr io.Writer
}

// Runtime binds one discovered descriptor to its native factories.
// SessionCapabilityContext and ExecutorCapabilityContext request the Runtime's
// capability-download URL and scoped product-upload context for those factories.
// Preparation never receives those execution-only effects.
type Runtime struct {
	Info                      proto.SupportedAgentKind
	Session                   Factory
	Preparation               PreparationFactory
	Executor                  ExecutorFactory
	WorkspaceReadPreparation  bool
	SessionCapabilityContext  bool
	ExecutorCapabilityContext bool
	// View declares how the Harness runs in an agent-host Session view.
	// A nil View means the agent host rejects the kind with ErrUnsupportedOperation.
	View *View
}

// Register installs a discovered Runtime with its declaration's configuration.
func (r *Registry) Register(declaration Declaration, runtime Runtime) {
	if runtime.Info.Kind != declaration.Info.Kind {
		panic("agent.Registry.Register: discovery kind differs from declaration")
	}
	r.RegisterKind(runtime.Info, declaration.Configuration, runtime.Session)
	if runtime.Executor != nil {
		r.RegisterExecutor(runtime.Info.Kind, runtime.Executor)
	}
	if runtime.Preparation != nil {
		r.RegisterPreparation(runtime.Info.Kind, runtime.WorkspaceReadPreparation, runtime.Preparation)
	}
	if runtime.View != nil {
		r.RegisterView(runtime.Info.Kind, declaration.ConnectionOptions, *runtime.View)
	}
}

// Agent-host Session view. The agent host runs the Harness in a per-Session
// view: the sandbox world at /, the closure, home and shims under
// ViewPrivateRoot, and a loopback-only network whose model, MCP and proxy
// endpoints belong to the Session's credential gateway. The declaration is
// data; the agent host builds each view from it and the Session.

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
	// ViewProcRoot and ViewDevRoot are the view's own /proc and minimal /dev.
	ViewProcRoot = "/proc"
	ViewDevRoot  = "/dev"
)

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
// than the Session's gateway, MCP outside ViewSession.MCP, an MCP credential or
// a connection option.
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
	// no HTTPHeaders; the gateway adds them. A stdio binding is as resolved and
	// runs in the sandbox through the declared shims. A view Executor takes MCP
	// only from here.
	MCP []MCPBinding
	// Launch replaces clirunner.Start. Each call builds one view and runs
	// Binary, which must be a LocalExec path, in it. Dir is a world path,
	// OwnProcessGroup is true, and Env is the complete Harness environment.
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
	// Parent bounds the wait for the start. Cancel sends TERM to that group
	// and kills it after KillTimeout; once the process has exited, Cancel
	// delivers nothing and what it left runs on as other processes in the view
	// do. The view's end ends them all: a Cancel of the Harness reaches them,
	// and when the Harness exits they are among the processes that remain. A
	// view that ends after Spawn returned shows in the process's Wait. Spawn
	// returns ErrNotLocalExec or ErrNoLiveView.
	Spawn func(clirunner.StartOptions) (*clirunner.Process, error)
}

// checkViewHandoff enforces, before the factory runs, that the view request
// reaches the network only through the Session's gateway: the model provider
// is the gateway with the placeholder key, MCP arrives only in session.MCP and
// without credentials, and no connection option is set.
func checkViewHandoff(req proto.PromptRequestPayload, prepared harnessconfig.PreparedConfiguration, connection []string, session ViewSession) error {
	if provider := prepared.Provider; provider == nil || provider.APIKey != modelprovider.Placeholder || !isGatewayURL(provider.BaseURL, false) {
		return fmt.Errorf("%w: the model provider is not the Session's gateway", ErrViewHandoff)
	}
	for _, key := range connection {
		if _, set := req.AgentOptions[key]; set {
			return fmt.Errorf("%w: option %q", ErrViewHandoff, key)
		}
	}
	if req.MCPHTTPServers != nil || (req.LocalEnvironment != nil && len(req.LocalEnvironment.MCP) > 0) {
		return fmt.Errorf("%w: MCP outside ViewSession.MCP", ErrViewHandoff)
	}
	for _, binding := range session.MCP {
		if binding.BearerToken != nil || len(binding.HTTPHeaders) > 0 || (binding.Transport == "http" && !isGatewayURL(binding.ServerURL, true)) {
			return fmt.Errorf("%w: MCP binding %q is not a credential-free gateway endpoint", ErrViewHandoff, binding.ServerLabel)
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
		if !isPathComponent(n) || n == ViewRelayName || slices.Contains(v.Shims[:i], n) {
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
// RegisterKind. connection is the declaration's ConnectionOptions. Its
// Executor factory validates the model configuration like RegisterExecutor and
// then checks the request against the view's gateway rule.
func (r *Registry) RegisterView(kind string, connection []string, view View) {
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
	connection = slices.Clone(connection)
	factory := view.Executor
	view.Executor = func(ctx context.Context, req proto.PromptRequestPayload, session ViewSession) (Executor, error) {
		prepared, err := configuration.Prepare(req.AgentOptions)
		if err != nil {
			return nil, err
		}
		if err := checkViewHandoff(req, prepared, connection, session); err != nil {
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

// Model configuration has one shared contract, authored in
// internal/harnessconfig/harness.go. RegisterKind requires that declaration;
// RegisterExecutor and RegisterPreparation inherit it. Every registered entry
// validates model, provider and native parameters before calling native code.
// The declaration belongs to the adapter and is also consumed by Core. Keep
// adapter field rules and rendering private. That shared contract owns frozen
// configuration and native application obligations. This file owns execution
// lifecycle only. Native image/tool/operation support is qualified through proto
// capabilities and the Core engine profile, not model configuration declarations.
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
type Turn interface {
	Session
	DurableSteerer
	// Success confirms closed output and settled native input, function,
	// interaction and child-work obligations. Errors cannot prove cancellation.
	AwaitSettlement(context.Context) (TurnSettlement, error)
}

// TurnSettlement describes native readiness after this Turn has fully drained.
// A false result must carry a reason; it requires confirmed Executor.Close.
type TurnSettlement struct {
	Reusable bool
	Reason   string
}

// Session is the cancellation and outcome surface shared by direct prompt runs
// and Turns. Every owner exposes observed state, including direct-call sessions.
// For Executor-owned Turns, AwaitSettlement and Executor.Close define settlement
// and resource retirement; Cancel alone does not transfer resource ownership.
type Session interface {
	// Cancel signals the session to abort. Idempotent. Actual teardown
	// happens asynchronously and is signalled via the out channel close.
	Cancel(ctx context.Context) error
	// CancellationOutcome snapshots observed native identity, usage and output.
	// It remains readable after Cancel; missing evidence stays unset. An empty
	// result means no observed evidence, not unsupported cancellation or success.
	// Reading it does not wait for or establish native settlement.
	CancellationOutcome() proto.DonePayload
}

// Turn extension contracts. Every public Harness implements each interface;
// unsupported operations return ErrUnsupportedOperation with a fixed safe reason.

// DurableSteerer reports one complete write synchronously, then waits for the native receipt.
// It is independent of Steerer and is mandatory on every public Turn.
// Required input receipts cannot be implemented by returning Unsupported.
type DurableSteerer interface {
	SteerWithReceipt(context.Context, proto.PromptSteerPayload, func()) error
}

// Steerer delivers non-durable input when qualified; otherwise it explicitly
// returns ErrUnsupportedOperation without submitting input.
type Steerer interface {
	Steer(context.Context, proto.PromptSteerPayload) error
}

// FunctionResultSubmitter delivers a result for an outstanding native call.
// A successful return requires its native application receipt.
type FunctionResultSubmitter interface {
	SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error
}

// PermissionResponder accepts decisions for qualified permission requests.
// An adapter that never supports these interactions returns ErrUnsupportedOperation.
// Unknown or expired requests return ErrUnknownPermission.
type PermissionResponder interface {
	SubmitPermission(context.Context, string, proto.PermissionDecisionPayload) error
}

// UserChoiceResponder accepts answers for qualified user-choice requests.
// An adapter that never supports these interactions returns ErrUnsupportedOperation.
// Unknown or expired requests return ErrUnknownAsk.
type UserChoiceResponder interface {
	SubmitPromptForUserChoice(context.Context, string, proto.PromptForUserChoiceDecisionPayload) error
}

// Workspace extensions, implemented by each owner explicitly. The common
// Runtime may supply an authorized workspace owner independently of the adapter.
// A resource without native access returns the corresponding Unsupported error;
// this does not disable capabilities provided by the common workspace owner.

// WorkspaceReader returns success only after acknowledged native close on an existing owner.
type WorkspaceReader interface {
	ReadWorkspaceFile(context.Context, string, int) (WorkspaceReadResult, error)
}

// WorkspaceDirectoryLister reads one directory through an existing workspace owner.
// An empty directory selects the root; other paths contain only relative components.
// Entry names are single components. Kind is file, directory, symlink or other;
// SizeBytes is present and nonnegative only for regular files. Results have no
// prescribed order, and Truncated must not be presented as a complete inventory.
// maxEntries is positive; adapters may reject limits above their private bound.
// Successful return requires settled directory/metadata access and handle cleanup.
// Implementations retain workspace authorization and isolation and use the existing
// WorkspaceRead errors for unsupported, unavailable, busy, invalid or uncertain reads.
// This interface does not establish public Files pagination or feature admission.
type WorkspaceDirectoryLister interface {
	ListWorkspaceDirectory(context.Context, string, int) (WorkspaceDirectoryResult, error)
}

// WorkspaceWriter confirms a native commit on an already authorized prepared owner.
type WorkspaceWriter interface {
	WriteWorkspaceFile(context.Context, string, []byte) (WorkspaceWriteResult, error)
}

// Separate preparation for qualified workspace access and direct-call paths.

// Prepared owns native resources until Start returns a non-nil Session. The
// preparation owner context spans the eventual Session; Start's context is local
// to that operation. A nil Session leaves preparation cleanup with the caller.
type Prepared interface {
	// Start transfers output ownership only when it returns a non-nil Session.
	// A nil Session leaves the caller as the sole owner of closing out, and the
	// implementation must not retain or write to it after Start returns.
	Start(context.Context, string, proto.MessageInput, chan<- proto.Envelope) (Session, error)
	// Close retains unused ownership on error; callers may retry settlement.
	Close() error
}

// PreparedCancellation is required for executable preparations and follows the
// same native resource across Start. Read-only preparations need only Prepared.
type PreparedCancellation interface {
	Prepared
	Session
	// Cancel returns after local cleanup and all output writes have stopped.
	// An error retains ownership so callers can retry this exact object serially.
	Cancel(context.Context) error
}

// A factory may return both a resource and an error when construction failed but
// cleanup remains unconfirmed. The caller must retain and close that resource.
type PreparationFactory func(context.Context, proto.PromptRequestPayload) (Prepared, error)

// Direct-call factory and registration. These use the existing Registry behavior.

// Factory builds a Session for one prompt_request. out is the upstream
// channel the agent writes into and closes exactly once after terminal output.
// ctx is cancelled by the router to wind the session down.
type Factory func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (Session, error)

// RegisterKind installs f and the heartbeat descriptor for an
// agent_kind. Callers may set Available=false when an adapter exists
// but its underlying CLI is not usable.
func (r *Registry) RegisterKind(info proto.SupportedAgentKind, configuration harnessconfig.Configuration, f Factory) {
	kind := info.Kind
	if kind == "" {
		panic("agent.Registry.Register: empty kind")
	}
	if f == nil {
		panic("agent.Registry.Register: nil factory")
	}
	if err := configuration.ValidateDeclaration(); err != nil {
		panic(err)
	}
	if err := info.ValidateDeclaration(); err != nil {
		panic(err)
	}
	configuration = configuration.Clone()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configurations[kind] = configuration
	r.factories[kind] = func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (Session, error) {
		if _, err := configuration.Prepare(req.AgentOptions); err != nil {
			return nil, err
		}
		return f(ctx, req, out)
	}
	delete(r.preparers, kind)
	delete(r.executors, kind)
	delete(r.views, kind)
	info.Capabilities.Preparation = proto.CapabilityUnsupported
	info.Capabilities.WorkspaceReadPreparation = proto.CapabilityUnsupported
	r.kinds[kind] = info
}

// RegisterExecutor installs the shared lifecycle after RegisterKind and derives
// the Preparation capability. It does not enable other public operations.
func (r *Registry) RegisterExecutor(kind string, factory ExecutorFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, exists := r.kinds[kind]
	if !exists || factory == nil {
		panic("agent.Registry.RegisterExecutor: registered kind and factory required")
	}
	configuration, declared := r.configurations[kind]
	if !declared {
		panic("agent.Registry.RegisterExecutor: configuration required")
	}
	r.executors[kind] = func(ctx context.Context, req proto.PromptRequestPayload) (Executor, error) {
		if _, err := configuration.Prepare(req.AgentOptions); err != nil {
			return nil, err
		}
		return factory(ctx, req)
	}
	info.Capabilities.Preparation = proto.CapabilitySupported
	r.kinds[kind] = info
}

// RegisterPreparation installs a separate execution-only path. Product factory
// wrappers must not add authoring or capability-download side effects to it.
func (r *Registry) RegisterPreparation(kind string, workspaceRead bool, prepare PreparationFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, exists := r.kinds[kind]
	if !exists || prepare == nil {
		panic("agent.Registry.RegisterPreparation: registered kind and factory required")
	}
	configuration, declared := r.configurations[kind]
	if !declared {
		panic("agent.Registry.RegisterPreparation: configuration required")
	}
	r.preparers[kind] = func(ctx context.Context, req proto.PromptRequestPayload) (Prepared, error) {
		if _, err := configuration.Prepare(req.AgentOptions); err != nil {
			return nil, err
		}
		return prepare(ctx, req)
	}
	info.Capabilities.Preparation = proto.CapabilitySupported
	info.Capabilities.WorkspaceReadPreparation = proto.CapabilityFromBool(workspaceRead)
	r.kinds[kind] = info
}
