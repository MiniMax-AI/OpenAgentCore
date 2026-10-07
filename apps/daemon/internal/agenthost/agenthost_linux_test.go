//go:build linux

package agenthost

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/google/uuid"
)

// The test binary is also the privileged suite's Harness inside the view,
// the view's shim and process relay, and its process with a zombie leader.
func TestMain(m *testing.M) {
	sessionview.Init()
	if processshim.Relaying() {
		os.Exit(processshim.Relay())
	}
	// The shim runs with the Harness's environment, so it comes first.
	if base := filepath.Base(os.Args[0]); base == "sh" || strings.HasPrefix(base, "oac-mcp-") {
		os.Exit(processshim.Run(processshim.SocketPath))
	}
	if os.Getenv(harnessEnv) != "" {
		os.Exit(runHarness(os.Args[1:]))
	}
	if os.Getenv(zombieLeaderEnv) != "" {
		runZombieLeader()
	}
	os.Exit(m.Run())
}

// newConfig returns a Config whose CA directory holds ca. Its ViewCgroups is
// a plain directory, which Open rejects.
func newConfig(t *testing.T, reg *agent.Registry, ca *x509.Certificate) Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{StateDir: t.TempDir(), UIDs: UIDRange{First: 70000, Count: 8}, ViewCgroups: t.TempDir(), RelayURL: "ws://127.0.0.1:9",
		RuntimeID: sandboxwire.NewID(), Credential: []byte("runtime-credential"), Harnesses: reg, Shim: exe, CADir: dir}
}

// register declares kind with view, or without one when view is nil, as
// Config.Harnesses holds it.
func register(reg *agent.Registry, kind string, view *agent.View) {
	info := proto.SupportedAgentKind{Kind: kind, Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
		LocalEnvironment: proto.CapabilitySupported, EnvironmentNone: proto.CapabilitySupported, MCPHTTPTools: proto.CapabilitySupported, MCPHTTPBearerAuth: proto.CapabilitySupported})}
	configuration := prototest.ModelConfiguration()
	configuration.Providers[0].Protocol = string(modelprovider.Anthropic)
	reg.RegisterKind(info, configuration)
	if view != nil {
		reg.RegisterView(kind, *view)
	}
}

// request is a Session request the agent host admits.
func request(kind, workspace, baseURL, key string) proto.PromptRequestPayload {
	return proto.PromptRequestPayload{
		AgentKind:        kind,
		Model:            "m",
		ModelProvider:    &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: baseURL, APIKey: key},
		LocalEnvironment: &proto.LocalEnvironment{WorkspaceDirectory: workspace, CapabilitySources: &agentcapabilities.Input{}},
	}
}

// prepared is req as the registry and the Environment owner hand it to the
// agent host's factory.
func prepared(req proto.PromptRequestPayload) agent.PrepareRequest {
	p := agent.PrepareRequest{PromptRequestPayload: req, Prepared: harnessconfig.PreparedConfiguration{Model: req.Model, Provider: *req.ModelProvider}}
	if req.LocalEnvironment != nil {
		p.WorkspaceRoot = req.LocalEnvironment.WorkspaceDirectory
	}
	return p
}

// newBinding returns the binding of a new Session on resource.
func newBinding(resource sandboxlink.ResourceRef) Binding {
	return Binding{Resource: resource, SessionID: sandboxwire.NewID(), AssignmentID: sandboxwire.NewID(), AssignmentEpoch: 1,
		AttachGrant: []byte("grant-" + sandboxwire.NewID().String())}
}

// bindTo binds every request to b.
func bindTo(b Binding) func(agent.PrepareRequest) (Binding, Environment, error) {
	return func(agent.PrepareRequest) (Binding, Environment, error) { return b, Environment{}, nil }
}

func newResource() sandboxlink.ResourceRef {
	return sandboxlink.ResourceRef{TenantID: sandboxwire.NewID(), EnvironmentID: sandboxwire.NewID(), Kind: sandboxlink.ResourceAllocation,
		ID: sandboxwire.NewID(), Generation: 1}
}

// countingDial counts dials and connects nothing.
func countingDial(n *atomic.Int32) dialFunc {
	return func(context.Context, func(sandboxlink.AttachmentClosed)) (*sandboxlink.AttachLink, error) {
		n.Add(1)
		return nil, errors.New("no relay in this test")
	}
}

// leftEntries lists what remains in the Session directories other than
// their homes.
func leftEntries(t *testing.T, cfg Config) []string {
	t.Helper()
	dirs, err := os.ReadDir(sessionsDir(cfg.StateDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var left []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(filepath.Join(sessionsDir(cfg.StateDir), dir.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name() != homeEntry {
				left = append(left, filepath.Join(dir.Name(), e.Name()))
			}
		}
	}
	return left
}

// daemon drives Sessions through a dispatch Router, as the daemon does, with
// a Host's Environment owners and Executor factory. It records the latest
// Executor the agent host opened for each Session.
type daemon struct {
	host   *Host
	router *dispatch.Router
	// mcp is the installed MCP that the Environment's preparation resolves
	// into each request; the wire does not carry it.
	mcp    []agent.EnvironmentMCP
	mu     sync.Mutex
	frames map[string]chan proto.Envelope // by envelope ID
	opened map[string]*session            // by Session ID
}

func newDaemon(t *testing.T, cfg Config, d deps) *daemon {
	t.Helper()
	dm := &daemon{host: &Host{cfg: cfg, owners: owners{d: d}}}
	dm.route(t, registry(cfg.Harnesses, func(ctx context.Context, req agent.PrepareRequest) (agent.Executor, error) {
		if dm.mcp != nil {
			req.MCP = dm.mcp
		}
		e, err := dm.host.openExecutor(ctx, req)
		if s, ok := e.(*session); ok {
			dm.mu.Lock()
			dm.opened[req.Assignment.SessionID] = s
			dm.mu.Unlock()
		}
		return e, err
	}))
	return dm
}

// route serves dm's Sessions through a new Router that runs reg, with the
// Host's Environment owners.
func (dm *daemon) route(t *testing.T, reg *agent.Registry) {
	t.Helper()
	dm.frames, dm.opened = map[string]chan proto.Envelope{}, map[string]*session{}
	var err error
	if dm.router, err = dispatch.New(dispatch.Config{Registry: reg, Sender: dm, Environments: dm.host.Environments, RemoveHome: dm.host.RemoveHome, Log: slog.New(slog.DiscardHandler)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dm.shutdown() })
}

// ref is the reference of b's assignment.
func ref(b Binding) proto.AssignmentRef {
	return proto.AssignmentRef{SessionID: uuid.UUID(b.SessionID).String(), AssignmentID: uuid.UUID(b.AssignmentID).String(), Epoch: b.AssignmentEpoch}
}

// environmentID is the Environment of b's resource; empty for environment
// none, whose binding has no resource.
func environmentID(b Binding) string {
	if b.Resource == (sandboxlink.ResourceRef{}) {
		return ""
	}
	return uuid.UUID(b.Resource.EnvironmentID).String()
}

// bindPayload is the assignment_bind that binds b's Session.
func bindPayload(b Binding) proto.AssignmentBindPayload {
	p := proto.AssignmentBindPayload{EnvironmentID: environmentID(b)}
	if p.EnvironmentID != "" {
		r := b.Resource
		kind := map[sandboxlink.ResourceKind]string{sandboxlink.ResourceAllocation: "allocation", sandboxlink.ResourceEnrollment: "enrollment"}[r.Kind]
		p.Resource = &sandboxbootstrap.Resource{TenantID: uuid.UUID(r.TenantID).String(), EnvironmentID: p.EnvironmentID, Kind: kind,
			ID: uuid.UUID(r.ID).String(), Generation: r.Generation}
		p.AttachGrant = b.AttachGrant
	}
	return p
}

func (dm *daemon) Send(_ context.Context, e proto.Envelope) error {
	dm.frame(e.ID) <- e
	return nil
}

func (dm *daemon) frame(id string) chan proto.Envelope {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	ch := dm.frames[id]
	if ch == nil {
		ch = make(chan proto.Envelope, 64)
		dm.frames[id] = ch
	}
	return ch
}

// handle hands the Router an envelope from Core under the assignment ref.
func (dm *daemon) handle(t *testing.T, ref proto.AssignmentRef, typ, id string, payload any) {
	t.Helper()
	e, err := proto.NewEnvelope(typ, id, payload)
	if err != nil {
		t.Fatal(err)
	}
	e.Assignment = ref
	if err := dm.router.Handle(context.Background(), e); err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
}

// next returns the next envelope sent with id.
func (dm *daemon) next(t *testing.T, id string) proto.Envelope {
	t.Helper()
	select {
	case e := <-dm.frame(id):
		return e
	case <-time.After(time.Minute):
		t.Fatalf("nothing sent for %s", id)
		return proto.Envelope{}
	}
}

// assign binds b's Session to the Router.
func (dm *daemon) assign(t *testing.T, b Binding) {
	t.Helper()
	id := sandboxwire.NewID().String()
	dm.handle(t, ref(b), proto.TypeAssignmentBind, id, bindPayload(b))
	if status := dm.status(t, id); status.State != proto.AssignmentBound {
		t.Fatalf("the bind is %s (%s), want bound", status.State, status.ErrorCode)
	}
}

// status returns the assignment status sent with id.
func (dm *daemon) status(t *testing.T, id string) proto.AssignmentStatusPayload {
	t.Helper()
	var status proto.AssignmentStatusPayload
	if err := dm.next(t, id).DecodePayload(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// prepare binds b's Session and prepares an Executor of it for req in the
// Environment of b's resource. It returns the request ID and the
// preparation's first status other than preparing.
func (dm *daemon) prepare(t *testing.T, b Binding, req proto.PromptRequestPayload) (string, proto.PreparationStatusPayload) {
	t.Helper()
	dm.assign(t, b)
	session := ref(b).SessionID
	if req.LocalEnvironment != nil {
		local := *req.LocalEnvironment
		local.ID = environmentID(b)
		req.LocalEnvironment = &local
	}
	id := sandboxwire.NewID().String()
	dm.handle(t, ref(b), proto.TypeExecutionPrepare, id, proto.ExecutionPreparePayload{SessionID: session, Configuration: req})
	for {
		var p proto.PreparationStatusPayload
		if err := dm.next(t, id).DecodePayload(&p); err != nil {
			t.Fatal(err)
		}
		if p.State != "preparing" {
			return id, p
		}
	}
}

// start prepares b's Session for req and starts a Turn whose input is text.
// It returns the Turn's RunID once the Turn has started.
func (dm *daemon) start(t *testing.T, b Binding, req proto.PromptRequestPayload, text string) string {
	t.Helper()
	id, p := dm.prepare(t, b, req)
	if p.State != "ready" {
		t.Fatalf("the preparation is %s (%s), want ready", p.State, p.ErrorCode)
	}
	run := "run-" + id
	dm.handle(t, ref(b), proto.TypeExecutionStart, id, proto.ExecutionStartPayload{Handle: p.Handle, ExecutorID: p.ExecutorID, RunID: run, Input: proto.TextInput(text)})
	for {
		if err := dm.next(t, id).DecodePayload(&p); err != nil {
			t.Fatal(err)
		}
		switch p.State {
		case "starting":
		case "started":
			return run
		default:
			t.Fatalf("the start is %s (%s), want started", p.State, p.ErrorCode)
		}
	}
}

// done returns what the Turn sends up to its Done.
func (dm *daemon) done(t *testing.T, run string) []proto.Envelope {
	t.Helper()
	var sent []proto.Envelope
	for {
		e := dm.next(t, run)
		if sent = append(sent, e); e.Type == proto.TypeDone {
			return sent
		}
	}
}

// session returns the latest Executor the agent host opened for b's Session.
func (dm *daemon) session(b Binding) *session {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.opened[ref(b).SessionID]
}

// shutdown shuts the Router down, which closes every Executor, and returns
// what their Close returned.
func (dm *daemon) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return dm.router.Shutdown(ctx)
}

// failures records each error the agent host logs.
type failures struct {
	mu   sync.Mutex
	errs []error
}

func (f *failures) Enabled(context.Context, slog.Level) bool { return true }

func (f *failures) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		if err, ok := a.Value.Any().(error); ok {
			f.mu.Lock()
			f.errs = append(f.errs, err)
			f.mu.Unlock()
		}
		return true
	})
	return nil
}

func (f *failures) WithAttrs([]slog.Attr) slog.Handler { return f }

func (f *failures) WithGroup(string) slog.Handler { return f }

// take returns the errors recorded since the last take, joined.
func (f *failures) take() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := errors.Join(f.errs...)
	f.errs = nil
	return err
}

// noTasks lists no running task.
func noTasks() ([][4]uint32, error) { return nil, nil }
