//go:build linux

package agenthost

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
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
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
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

// register declares kind with view, or without one when view is nil.
func register(reg *agent.Registry, kind string, view *agent.View) {
	info := proto.SupportedAgentKind{Kind: kind, Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
		MCPHTTPTools: proto.CapabilitySupported, MCPHTTPBearerAuth: proto.CapabilitySupported, WorkspaceReadPreparation: proto.CapabilitySupported})}
	declaration := agent.Declaration{Info: info,
		Configuration: harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: string(modelprovider.Anthropic)}}}}
	reg.Register(declaration, agent.Runtime{Info: info, View: view,
		Session: func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, errors.New("not used")
		}})
}

// declared declares every view capability as s.
func declared(s proto.CapabilitySupport) agent.ViewCapabilities {
	return agent.ViewCapabilities{EnvironmentNone: s, Skills: s, FunctionTools: s, FunctionResultImages: s, ToolSearch: s, StdioMCP: s}
}

// request is a Session request the agent host admits.
func request(kind, workspace, baseURL, key string) proto.PromptRequestPayload {
	return proto.PromptRequestPayload{
		AgentKind:        kind,
		StrictResume:     true,
		Model:            "m",
		ModelProvider:    &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: baseURL, APIKey: key},
		LocalEnvironment: &proto.LocalEnvironment{WorkspaceDirectory: workspace, NetworkAccess: "enabled"},
	}
}

// newBinding returns the binding of a new Session on resource.
func newBinding(resource sandboxlink.ResourceRef) Binding {
	return Binding{Resource: resource, SessionID: sandboxwire.NewID(), AssignmentID: sandboxwire.NewID(), AssignmentEpoch: 1,
		AttachGrant: []byte("grant-" + sandboxwire.NewID().String())}
}

// bindTo binds every request to b.
func bindTo(b Binding) func(proto.PromptRequestPayload) (Binding, Environment, error) {
	return func(proto.PromptRequestPayload) (Binding, Environment, error) { return b, Environment{}, nil }
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

// daemon drives Sessions through a dispatch Router, as the daemon does. It
// binds each request to the Session of the assignment the Router admitted it
// under, records the latest Executor the agent host opened for each Session,
// and removes a released Session's home.
type daemon struct {
	router *dispatch.Router
	// mcp is the installed MCP that the Environment's preparation resolves
	// into each request; the wire does not carry it.
	mcp      []proto.EnvironmentMCP
	mu       sync.Mutex
	frames   map[string]chan proto.Envelope // by envelope ID
	bindings map[string]Binding             // by Session ID
	opened   map[string]*session            // by Session ID
}

func newDaemon(t *testing.T, cfg Config, d deps) *daemon {
	t.Helper()
	dm := &daemon{frames: map[string]chan proto.Envelope{}, bindings: map[string]Binding{}, opened: map[string]*session{}}
	reg := registry(cfg.Harnesses, func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
		if dm.mcp != nil {
			local := *req.LocalEnvironment
			local.MCP = dm.mcp
			req.LocalEnvironment = &local
		}
		e, err := open(ctx, cfg, req, dm.bind, d)
		if s, ok := e.(*session); ok {
			dm.mu.Lock()
			dm.opened[strings.TrimPrefix(req.AgentStateKey, stateKeyPrefix)] = s
			dm.mu.Unlock()
		}
		return e, err
	})
	var err error
	removeHome := func(session string) error {
		dm.mu.Lock()
		b, ok := dm.bindings[session]
		dm.mu.Unlock()
		if !ok {
			return fmt.Errorf("%w: no binding", ErrInvalidSession)
		}
		return (&Host{cfg: cfg}).RemoveHome(b.SessionID)
	}
	if dm.router, err = dispatch.New(dispatch.Config{Registry: reg, Sender: dm, SessionEnvironments: true, RemoveHome: removeHome, Log: slog.New(slog.DiscardHandler)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dm.shutdown() })
	return dm
}

// stateKeyPrefix and the Session ID make the state key dispatch requires.
const stateKeyPrefix = "agents-api-"

func (dm *daemon) bind(req proto.PromptRequestPayload) (Binding, Environment, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	b, ok := dm.bindings[req.Assignment.SessionID]
	if !ok || ref(b) != req.Assignment {
		return Binding{}, Environment{}, fmt.Errorf("%w: no binding", ErrInvalidSession)
	}
	return b, Environment{}, nil
}

// ref is the reference of b's assignment.
func ref(b Binding) proto.AssignmentRef {
	return proto.AssignmentRef{SessionID: b.SessionID.String(), AssignmentID: b.AssignmentID.String(), Epoch: b.AssignmentEpoch}
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

// assign binds b's Session to the Router in environment.
func (dm *daemon) assign(t *testing.T, b Binding, environment string) {
	t.Helper()
	dm.mu.Lock()
	dm.bindings[b.SessionID.String()] = b
	dm.mu.Unlock()
	id := sandboxwire.NewID().String()
	dm.handle(t, ref(b), proto.TypeAssignmentBind, id, proto.AssignmentBindPayload{EnvironmentID: environment})
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

// prepare binds b's Session and prepares an Executor of it for req. It
// returns the request ID and the preparation's first status other than
// preparing.
func (dm *daemon) prepare(t *testing.T, b Binding, req proto.PromptRequestPayload) (string, proto.PreparationStatusPayload) {
	t.Helper()
	dm.assign(t, b, req.EnvironmentID())
	session := b.SessionID.String()
	req.AgentStateKey = stateKeyPrefix + session
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
	return dm.opened[b.SessionID.String()]
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
