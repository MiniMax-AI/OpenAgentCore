//go:build linux

package agenthost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// deps are the parts tests replace.
type deps struct {
	dial  dialFunc
	tasks listTasks
}

// Registry returns the kinds the agent host runs, for the daemon's dispatch
// and heartbeat: each kind in Config.Harnesses that declares an agent.View,
// with Info that describes how views run it. Its Executor factory prepares an
// Executor of the Session that bind binds the request to, as the package
// documentation describes, and bind's error fails the preparation. The Router
// that runs it sets dispatch.Config.SessionEnvironments. A direct prompt run
// is unsupported.
func (h *Host) Registry(bind func(proto.PromptRequestPayload) (Binding, Environment, error)) *agent.Registry {
	d := deps{dial: relayDial(h.cfg), tasks: taskUIDs}
	return registry(h.cfg.Harnesses, func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
		return open(ctx, h.cfg, req, bind, d)
	})
}

// registry registers each kind in harnesses that declares a view, with
// factory as its Executor factory.
func registry(harnesses *agent.Registry, factory agent.ExecutorFactory) *agent.Registry {
	reg := agent.NewRegistry()
	for _, info := range harnesses.SupportedAgentKinds() {
		configuration, err := harnesses.Configuration(info.Kind)
		if _, viewErr := harnesses.ResolveView(info.Kind); err != nil || viewErr != nil {
			continue
		}
		reg.RegisterKind(viewInfo(info), configuration, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, unsupported("a direct prompt run")
		})
		reg.RegisterExecutor(info.Kind, factory)
	}
	return reg
}

// viewInfo is info as views run the kind: in the Session's Environment, and
// without what admission rejects or what needs a local workspace.
func viewInfo(info proto.SupportedAgentKind) proto.SupportedAgentKind {
	c := &info.Capabilities
	c.LocalEnvironment = proto.CapabilitySupported
	for _, field := range []*proto.CapabilitySupport{&c.EnvironmentNone, &c.ToolSearch, &c.FunctionTools, &c.FunctionResultImages,
		&c.WorkspaceAuthoring, &c.WorkspaceOutputExport} {
		*field = proto.CapabilityUnsupported
	}
	return info
}

// session is an Executor of a Session on the agent host: the view Executor
// with the views, the Link attachment, the transient entries, the uid and the
// Session's claim that Close releases.
type session struct {
	cfg  Config
	env  Environment
	plan *plan
	link *linkOwner
	log  *slog.Logger
	id   sandboxwire.ID // the Session's
	uid  uint32
	dir  sessionDir
	// exec is the view Executor; nil when its factory returned none.
	exec agent.Executor

	// ctx ends when the Session fails or Close ends it.
	ctx    context.Context
	cancel context.CancelFunc

	failMu sync.Mutex
	left   error // a view whose teardown did not finish

	mu sync.Mutex
	// live is the one view that may run; nil when none does.
	live *liveView
	// views counts launches and their views until each is torn down.
	views sync.WaitGroup

	closeMu    sync.Mutex
	execClosed bool // a Close closed the view Executor
	released   bool // a Close released everything
}

// open prepares an Executor of the Session that bind binds req to. It admits
// the request before any effect, then claims the Session, allocates the
// Executor's uid, prepares the Session directory and calls the view's
// Executor factory. Once it has an effect, it returns the session even when
// it fails, and the session's Close releases what it holds.
func open(ctx context.Context, cfg Config, req proto.PromptRequestPayload, bind func(proto.PromptRequestPayload) (Binding, Environment, error), d deps) (agent.Executor, error) {
	roots, err := checkConfig(cfg)
	if err != nil {
		return nil, err
	}
	b, env, err := bind(req)
	if err != nil {
		return nil, err
	}
	s := &session{cfg: cfg, env: env, log: cfg.Log}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	s.link = newLinkOwner(d.dial, b, sandboxwire.NewID(), s.fail)
	if err := checkBinding(s.link.request(sandboxlink.ServiceFile, sandboxfs.Version, sandboxwire.ID{}), env); err != nil {
		return nil, err
	}
	if s.plan, err = admit(cfg, roots, req, env, s.openNetwork); err != nil {
		return nil, err
	}
	if !claimSession(b.SessionID) {
		return nil, fmt.Errorf("%w: an Executor of the Session has not closed, or its home is being removed", ErrSessionExists)
	}
	s.id = b.SessionID
	if s.uid, err = allocUID(cfg.UIDs, d.tasks); err != nil {
		releaseSession(s.id)
		return nil, err
	}
	if s.dir, err = openSessionDir(cfg.StateDir, s.id, s.uid); err != nil {
		freeUID(s.uid)
		releaseSession(s.id)
		return nil, err
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	context.AfterFunc(s.ctx, s.closeLive)
	s.exec, err = s.plan.view.Executor(s.ctx, s.plan.request, agent.ViewSession{
		Home:   agent.ViewDir{Host: s.dir.entry(homeEntry), View: agent.ViewPrivateRoot + "/" + agent.ViewHomeName},
		Proxy:  s.plan.proxy,
		MCP:    s.plan.mcp,
		Launch: s.launch,
		Spawn:  s.spawn,
	})
	if err != nil {
		if errors.Is(err, agent.ErrUnsupportedOperation) || errors.Is(err, agent.ErrViewHandoff) {
			return s, fmt.Errorf("%w: executor: %w", ErrUnsupported, err)
		}
		return s, fmt.Errorf("%w: %w", ErrExecutor, err)
	}
	return s, nil
}

// StartTurn starts a Turn of the view Executor. A Session that failed starts
// none, so dispatch closes the Executor.
func (s *session) StartTurn(ctx context.Context, runID string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	if s.ctx.Err() != nil {
		return nil, fmt.Errorf("%w: the Session failed", ErrLaunch)
	}
	return s.exec.StartTurn(ctx, runID, input, out)
}

// Close tears the Executor down in order: the view Executor, the views with
// their process brokers, the Link attachment, the transient entries, and the
// uid with the Session's claim. When the view Executor's Close fails, Close
// ends the views, which kills their processes, and retries it once. If the
// view Executor, the attachment or the transient entries still remain, Close
// returns ErrTeardown and keeps them, the uid and the claim, and a later
// Close retries what remains. A view whose teardown did not finish may leave
// processes that use the Session directory: then every Close returns
// ErrTeardown, and the uid and the claim stay until the agent host exits.
func (s *session) Close(ctx context.Context) error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.released {
		return nil
	}
	var err error
	if s.exec != nil && !s.execClosed {
		err = s.exec.Close(ctx)
	}
	// Ending the Session closes the live view and refuses new launches. Under
	// mu, every launch that passed its check has already counted itself.
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	s.views.Wait()
	if err != nil {
		err = s.exec.Close(ctx)
	}
	if err != nil {
		err = fmt.Errorf("%w: close executor: %w", ErrTeardown, err)
	} else {
		s.execClosed = true
	}
	// Until the relay confirms that the attachment is closed, what the
	// Session's processes started in the sandbox may still run.
	err = errors.Join(err, s.link.close())
	s.failMu.Lock()
	err = errors.Join(s.left, err)
	s.failMu.Unlock()
	if err != nil {
		return err
	}
	if err := s.dir.clear(); err != nil {
		return fmt.Errorf("%w: remove transient entries: %w", ErrTeardown, err)
	}
	freeUID(s.uid)
	releaseSession(s.id)
	s.released = true
	return nil
}

// fail records err, a failure that ends the Session: its live view closes,
// which ends the running Turn, and later launches fail. No Turn reports why,
// so fail logs it.
func (s *session) fail(err error) {
	s.log.Error("agent host session failed", "error", err)
	s.cancel()
}

// worldEnded records a world that did not stop cleanly, or that cannot show
// that its attachment holds nothing. Only ending the attachment settles its
// state, so the Session fails.
func (s *session) worldEnded(op string, err error) error {
	e := fmt.Errorf("%w: %s: %w", ErrWorld, op, err)
	s.fail(e)
	return e
}

// viewLeft records a view whose teardown did not finish within sessionview's
// bound (sessionview.ErrCleanup): its processes may still run and use the
// Session directory. The Session fails, and Close keeps the transient entries
// and the uid.
func (s *session) viewLeft(err error) error {
	e := fmt.Errorf("%w: view: %w", ErrTeardown, err)
	s.failMu.Lock()
	if s.left == nil {
		s.left = e
	}
	s.failMu.Unlock()
	s.fail(e)
	return e
}

func (s *session) openFile(ctx context.Context) (io.ReadWriteCloser, error) {
	st, err := s.link.open(ctx, sandboxlink.ServiceFile, sandboxfs.Version)
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (s *session) openProcess(ctx context.Context) (io.ReadWriteCloser, error) {
	st, err := s.link.open(ctx, sandboxlink.ServiceProcess, sandboxprocess.Version)
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (s *session) openNetwork(ctx context.Context) (sandboxlink.Stream, error) {
	return s.link.open(ctx, sandboxlink.ServiceNetwork, sandboxnet.Version)
}
