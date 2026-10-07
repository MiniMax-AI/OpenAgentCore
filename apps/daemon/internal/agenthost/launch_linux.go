//go:build linux

package agenthost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processbroker"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/worldfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

// worldExport is the File service export that holds the sandbox's world, as
// docs/sandbox-bootstrap.md defines it.
const worldExport sandboxlink.ExportID = "world"

// liveView is the Session's one live view slot.
type liveView struct {
	view   runningView // nil while the view is being built
	closed bool        // the Session ended while the view was being built
}

// runningView is the part of *sessionview.View the Session owns.
type runningView interface {
	Signal(syscall.Signal) error
	Wait() (sessionview.Exit, error)
	Close() error
	Relay() *os.File
	RelayLost() <-chan struct{}
	Spawn(ctx context.Context, path string, args, env []string, dir string, stdin bool) (*sessionview.Spawned, error)
}

// viewWorld is the part of *worldfs.World the Session watches.
type viewWorld interface {
	Stop() error
	Lost() <-chan struct{}
	Err() error
}

// closeLive closes the live view. It runs when the Session's context ends.
func (s *session) closeLive() {
	s.mu.Lock()
	lv := s.live
	var v runningView
	if lv != nil {
		lv.closed, v = true, lv.view
	}
	s.mu.Unlock()
	if v != nil {
		v.Close()
	}
}

// checkStart checks the options of Launch and Spawn.
func (s *session) checkStart(opts clirunner.StartOptions) error {
	switch {
	case !slices.Contains(s.plan.view.LocalExec, opts.Binary):
		return fmt.Errorf("%w: %w: %q", ErrLaunch, agent.ErrNotLocalExec, opts.Binary)
	case !isViewPath(opts.Dir):
		return fmt.Errorf("%w: directory %q is not absolute and clean", ErrLaunch, opts.Dir)
	case !opts.OwnProcessGroup:
		return fmt.Errorf("%w: a view process runs in its own process group", ErrLaunch)
	}
	return nil
}

// launch is ViewSession.Launch: it builds one view and runs opts.Binary in it.
func (s *session) launch(opts clirunner.StartOptions) (*clirunner.Process, error) {
	if err := s.checkStart(opts); err != nil {
		return nil, err
	}
	if opts.Parent == nil {
		opts.Parent = context.Background()
	}
	if opts.KillTimeout <= 0 {
		opts.KillTimeout = clirunner.DefaultKillTimeout
	}
	s.mu.Lock()
	switch {
	case s.ctx.Err() != nil:
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: the Session is ending", ErrLaunch)
	case s.live != nil:
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: the Session already has a live view", ErrLaunch)
	}
	lv := &liveView{}
	s.live = lv
	s.views.Add(1)
	s.mu.Unlock()
	return s.start(lv, opts)
}

// spawn is ViewSession.Spawn: it runs opts.Binary in the live view.
func (s *session) spawn(opts clirunner.StartOptions) (*clirunner.Process, error) {
	if err := s.checkStart(opts); err != nil {
		return nil, err
	}
	var v runningView
	s.mu.Lock()
	if s.live != nil {
		v = s.live.view
	}
	s.mu.Unlock()
	if v == nil {
		return nil, fmt.Errorf("%w: spawn: %w", ErrLaunch, agent.ErrNoLiveView)
	}
	if opts.Parent == nil {
		opts.Parent = context.Background()
	}
	p, err := v.Spawn(opts.Parent, opts.Binary, append([]string{opts.Binary}, opts.Args...), opts.Env, opts.Dir, opts.NeedStdin)
	if err != nil {
		// Only a view that has ended has no live view; any other failure keeps its own error.
		if errors.Is(err, sessionview.ErrExited) || errors.Is(err, sessionview.ErrClosed) {
			err = fmt.Errorf("%w: %w", agent.ErrNoLiveView, err)
		}
		return nil, fmt.Errorf("%w: spawn: %w", ErrLaunch, err)
	}
	// FromHandle fails only without stdout and stderr, which a spawned process always has.
	process, _ := clirunner.FromHandle(p, clirunner.HandleOptions{Parent: opts.Parent, Stdin: writer(p.Stdin), Stdout: p.Stdout, Stderr: p.Stderr, KillTimeout: opts.KillTimeout})
	return process, nil
}

// release frees the view slot and ends the launch's count.
func (s *session) release(lv *liveView) {
	s.mu.Lock()
	if s.live == lv {
		s.live = nil
	}
	s.mu.Unlock()
	s.views.Done()
}

// start builds the view for lv. Until the view runs, each failure releases
// lv; from then on the view's owner does.
func (s *session) start(lv *liveView, opts clirunner.StartOptions) (*clirunner.Process, error) {
	// Construction ends with the Session or with the caller.
	startCtx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	defer context.AfterFunc(opts.Parent, cancel)()
	view := s.plan.view
	var scope sandboxprocess.Scope
	if len(view.Shims) > 0 || len(view.ShimPaths) > 0 {
		var err error
		if scope, err = s.processScope(startCtx); err != nil {
			s.release(lv)
			if startCtx.Err() != nil {
				return nil, fmt.Errorf("%w: describe: %w", ErrLaunch, err)
			}
			return nil, s.brokerFailed("describe", err)
		}
	}
	if err := s.dir.chownHome(s.uid); err != nil {
		s.release(lv)
		return nil, fmt.Errorf("%w: home: %w", ErrLaunch, err)
	}
	child, ends, err := sessionview.Stdio([3]*os.File{}, opts.NeedStdin, s.uid, s.uid)
	if err != nil {
		s.release(lv)
		return nil, fmt.Errorf("%w: stdio: %w", ErrLaunch, err)
	}
	world := worldfs.New(worldExport, s.openFile)
	spec := s.spec(world, opts, child)
	// The gateway serves from the view's network hook until the view has ended.
	var stopGateway func()
	spec.Network.Setup = func(netns *os.File) (err error) {
		stopGateway, err = gateway.Start(netns, s.plan.gateway)
		return err
	}
	v, err := sessionview.Start(startCtx, spec)
	closeFiles(child[:])
	if err != nil {
		if stopGateway != nil {
			stopGateway()
		}
		closeFiles(ends[:])
		defer s.release(lv)
		// sessionview stops a world that served; Stop reports how that went.
		serr := world.Stop()
		var left error
		if errors.Is(err, sessionview.ErrCleanup) {
			left = s.viewLeft(err)
		}
		switch {
		case serr != nil || errors.Is(err, worldfs.ErrAttachmentDirty):
			return nil, s.worldEnded("launch", errors.Join(err, serr))
		case left != nil:
			return nil, left
		}
		return nil, fmt.Errorf("%w: %w", ErrLaunch, err)
	}
	p := v.Presentation()
	s.log.Info("agent host view started", "binary", opts.Binary, "targets", p.Targets, "links", p.Links, "synthesized", p.Synthesized)
	return s.own(lv, v, world, stopGateway, opts, ends, scope)
}

// processScope describes the Session's Process service and returns the
// scope a view's forwarded processes start in: the strongest one the service
// declares.
func (s *session) processScope(ctx context.Context) (sandboxprocess.Scope, error) {
	rw, err := s.openProcess(ctx)
	if err != nil {
		return 0, err
	}
	c := sandboxprocess.NewClient(rw)
	defer c.Close()
	d, err := c.Describe(ctx)
	if err != nil {
		return 0, err
	}
	for _, scope := range []sandboxprocess.Scope{sandboxprocess.ScopeCgroupV2, sandboxprocess.ScopePOSIXSession} {
		if slices.Contains(d.Capabilities.Scopes, scope) {
			return scope, nil
		}
	}
	return 0, fmt.Errorf("the Process service declares no scope among %v", d.Capabilities.Scopes)
}

// brokerFailed fails the Session with a process broker failure.
func (s *session) brokerFailed(op string, err error) error {
	e := fmt.Errorf("%w: %s: %w", ErrProcessBroker, op, err)
	s.fail(e)
	return e
}

// own hands a started view to the clirunner.Process the adapter receives. A
// view with a relay gets its own process broker, which serves the view's
// shims in scope until the view ends.
func (s *session) own(lv *liveView, v runningView, world viewWorld, stopGateway func(), opts clirunner.StartOptions, ends [3]*os.File, scope sandboxprocess.Scope) (*clirunner.Process, error) {
	h := &ownedView{s: s, lv: lv, v: v, world: world, stopGateway: stopGateway, ended: make(chan struct{}), watched: make(chan struct{})}
	var brokerErr error
	if relay := v.Relay(); relay != nil {
		h.broker, brokerErr = processbroker.Start(processbroker.Config{
			Relay:       relay,
			Executables: s.plan.executables,
			Environment: processbroker.Environment{Pass: s.plan.view.ForwardEnv, Sandbox: s.in.Environment.Sandbox, Tool: s.in.Environment.Tool},
			Scope:       scope,
			Dial:        s.openProcess,
			CancelGrace: opts.KillTimeout,
			Logger:      s.log,
		})
	}
	go h.watch()
	s.mu.Lock()
	lv.view = v
	closed := lv.closed
	s.mu.Unlock()
	var err error
	switch {
	case brokerErr != nil:
		err = s.brokerFailed("start", brokerErr)
	case closed:
		err = fmt.Errorf("%w: the Session is ending", ErrLaunch)
	}
	var process *clirunner.Process
	if err == nil {
		process, err = clirunner.FromHandle(h, clirunner.HandleOptions{Parent: opts.Parent, Stdin: writer(ends[0]),
			Stdout: ends[1], Stderr: ends[2], KillTimeout: opts.KillTimeout})
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrLaunch, err)
		}
	}
	if err != nil {
		v.Close()
		h.Wait()
		closeFiles(ends[:])
		return nil, err
	}
	return process, nil
}

// ownedView is a running view as a clirunner.Handle. Its Wait ends the
// Session's ownership of the view before it returns, so the end the adapter
// observes through the Process comes after it: the gateway and the process
// broker have stopped, a lost world, a lost relay or a view or world that
// did not stop cleanly has failed the Session, and the view slot is free for
// the next Launch.
type ownedView struct {
	s           *session
	lv          *liveView
	v           runningView
	world       viewWorld
	broker      *processbroker.Broker // nil when the view has no relay
	stopGateway func()
	ended       chan struct{} // closed once the view has ended
	watched     chan struct{} // closed when watch returns
	once        sync.Once
}

// watch fails the Session as soon as the world or the process relay is lost
// while the view runs.
func (h *ownedView) watch() {
	defer close(h.watched)
	var relayLost <-chan struct{}
	if h.broker != nil {
		relayLost = h.v.RelayLost()
	}
	select {
	case <-h.world.Lost():
		h.s.fail(fmt.Errorf("%w: world: %w", ErrWorld, h.world.Err()))
		return
	case <-relayLost:
	case <-h.ended:
		// A lost relay is reported before the view ends.
		select {
		case <-relayLost:
		default:
			return
		}
	}
	// The relay's end ends its connection, so the broker stops.
	<-h.broker.Done()
	h.s.log.Error("process relay lost", "error", h.broker.Err())
	h.s.brokerFailed("relay", h.broker.Err())
}

func (h *ownedView) Signal(sig syscall.Signal) error { return h.v.Signal(sig) }

func (h *ownedView) Close() error { return h.v.Close() }

func (h *ownedView) Wait() (int, error) {
	exit, err := h.v.Wait()
	h.once.Do(func() { h.end(err) })
	switch {
	case err != nil:
		return -1, err
	case exit.Signal != 0:
		return -1, nil
	}
	return exit.Code, nil
}

// end releases the view once it has ended and its world has stopped. waitErr
// is how the view's Wait ended.
func (h *ownedView) end(waitErr error) {
	h.stopGateway()
	close(h.ended)
	<-h.watched
	if h.broker != nil {
		if err := h.broker.Close(); err != nil {
			h.s.ended(fmt.Errorf("%w: close process broker: %w", ErrTeardown, err), false)
		}
	}
	if errors.Is(waitErr, sessionview.ErrCleanup) {
		h.s.viewLeft(waitErr)
	}
	if lost := h.world.Err(); lost != nil {
		h.s.fail(fmt.Errorf("%w: world: %w", ErrWorld, lost))
	}
	// The view has stopped its world; Stop reports how that went.
	if err := h.world.Stop(); err != nil {
		h.s.worldEnded("stop world", err)
	}
	h.s.release(h.lv)
}

// spec builds the view: the closure and home directories, the agent
// host's /etc files and CA directory, the adapter's overlays and masks, and
// the shim.
func (s *session) spec(world *worldfs.World, opts clirunner.StartOptions, stdio [3]*os.File) sessionview.Spec {
	view := s.plan.view
	var private []sessionview.PrivateDir
	for _, m := range view.Closure {
		private = append(private, sessionview.PrivateDir{Name: m.Name, HostDir: m.HostDir, Exec: true})
	}
	private = append(private, sessionview.PrivateDir{Name: agent.ViewHomeName, HostDir: s.dir.entry(homeEntry), Writable: true})
	var overlays []sessionview.Overlay
	for _, name := range etcFiles {
		overlays = append(overlays, sessionview.Overlay{Path: "/etc/" + name, Source: s.dir.entry(etcEntry, name)})
	}
	overlays = append(overlays, sessionview.Overlay{Path: s.cfg.CADir, Source: s.cfg.CADir})
	for _, o := range view.Overlays {
		overlays = append(overlays, sessionview.Overlay{Path: o.Path, Source: o.Source, Exec: o.Exec})
	}
	for _, m := range view.Masks {
		source := s.dir.entry(maskEntry, "file")
		if m.Dir {
			source = s.dir.entry(maskEntry, "dir")
		}
		overlays = append(overlays, sessionview.Overlay{Path: m.Path, Source: source})
	}
	return sessionview.Spec{
		World:    world.Serve,
		Private:  private,
		Overlays: overlays,
		Shim:     sessionview.Shim{Binary: s.cfg.Shim, Names: view.Shims, Paths: view.ShimPaths},
		Process: sessionview.Process{Path: opts.Binary, Args: append([]string{opts.Binary}, opts.Args...), Env: opts.Env,
			Dir: opts.Dir, UID: s.uid, GID: s.uid, Stdin: stdio[0], Stdout: stdio[1], Stderr: stdio[2],
			Grace: opts.KillTimeout},
		StagingParent: s.dir.entry(stagingEntry),
		CgroupParent:  s.cfg.ViewCgroups,
	}
}

// writer returns f, or nil without f: a nil *os.File would be a writer
// that fails.
func writer(f *os.File) io.WriteCloser {
	if f == nil {
		return nil
	}
	return f
}

func closeFiles(files []*os.File) {
	for _, f := range files {
		if f != nil {
			f.Close()
		}
	}
}
