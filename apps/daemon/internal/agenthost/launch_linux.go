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
		return &Error{Kind: ErrLaunch, Err: fmt.Errorf("%w: %q", agent.ErrNotLocalExec, opts.Binary)}
	case !isViewPath(opts.Dir):
		return &Error{Kind: ErrLaunch, Err: fmt.Errorf("directory %q is not absolute and clean", opts.Dir)}
	case !opts.OwnProcessGroup:
		return &Error{Kind: ErrLaunch, Err: errors.New("a view process runs in its own process group")}
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
		return nil, &Error{Kind: ErrLaunch, Err: errors.New("the Session is ending")}
	case s.live != nil:
		s.mu.Unlock()
		return nil, &Error{Kind: ErrLaunch, Err: errors.New("the Session already has a live view")}
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
		return nil, &Error{Kind: ErrLaunch, Op: "spawn", Err: agent.ErrNoLiveView}
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
		return nil, &Error{Kind: ErrLaunch, Op: "spawn", Err: err}
	}
	var stdin io.WriteCloser
	if p.Stdin != nil {
		stdin = p.Stdin
	}
	// FromHandle fails only without stdout and stderr, which a spawned process always has.
	process, _ := clirunner.FromHandle(p, clirunner.HandleOptions{Parent: opts.Parent, Stdin: stdin, Stdout: p.Stdout, Stderr: p.Stderr, KillTimeout: opts.KillTimeout})
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
				return nil, &Error{Kind: ErrLaunch, Op: "describe", Err: err}
			}
			return nil, s.brokerFailed("describe", err)
		}
	}
	if err := s.dir.chownHome(s.uid); err != nil {
		s.release(lv)
		return nil, &Error{Kind: ErrLaunch, Op: "home", Err: err}
	}
	ends, err := newStdio(opts.NeedStdin)
	if err != nil {
		s.release(lv)
		return nil, &Error{Kind: ErrLaunch, Op: "stdio", Err: err}
	}
	// The gateway serves until the view has ended.
	viewCtx, stopGateway := context.WithCancel(context.Background())
	world := worldfs.New(worldExport, s.openFile)
	spec := s.spec(viewCtx, world, opts, ends)
	v, err := sessionview.Start(startCtx, spec)
	ends.closeChild()
	if err != nil {
		stopGateway()
		ends.closeParent()
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
		return nil, &Error{Kind: ErrLaunch, Err: err}
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
	e := &Error{Kind: ErrProcessBroker, Op: op, Err: err}
	s.fail(e)
	return e
}

// own hands a started view to the clirunner.Process the adapter receives. A
// view with a relay gets its own process broker, which serves the view's
// shims in scope until the view ends.
func (s *session) own(lv *liveView, v runningView, world viewWorld, stopGateway func(), opts clirunner.StartOptions, ends *stdio, scope sandboxprocess.Scope) (*clirunner.Process, error) {
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
		err = &Error{Kind: ErrLaunch, Err: errors.New("the Session is ending")}
	}
	var process *clirunner.Process
	if err == nil {
		process, err = clirunner.FromHandle(h, clirunner.HandleOptions{Parent: opts.Parent, Stdin: ends.stdin(),
			Stdout: ends.parent[1], Stderr: ends.parent[2], KillTimeout: opts.KillTimeout})
		if err != nil {
			err = &Error{Kind: ErrLaunch, Err: err}
		}
	}
	if err != nil {
		v.Close()
		h.Wait()
		ends.closeParent()
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
	var relayEnded <-chan struct{}
	if h.broker != nil {
		relayEnded = h.broker.Done()
	}
	for {
		select {
		case <-h.world.Lost():
			h.s.fail(&Error{Kind: ErrWorld, Op: "world", Err: h.world.Err()})
			return
		case <-relayEnded:
			// The broker stops serving on Close, which end calls only after
			// watch returns, or when its relay connection ends. sessionview
			// ends that connection itself only in its teardown, which starts
			// once the launcher has stopped answering, and from then on
			// Signal fails with ErrExited, ErrClosed or a lost launcher. So a
			// delivered signal means that the view still runs its process
			// and the relay was lost while the Harness ran. A failed one
			// means that the view is ending, and Wait reports how.
			if h.v.Signal(0) == nil {
				h.s.brokerFailed("relay", h.broker.Err())
				return
			}
			relayEnded = nil
		case <-h.ended:
			return
		}
	}
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
			h.s.ended(&Error{Kind: ErrTeardown, Op: "close process broker", Err: err}, false)
		}
	}
	if errors.Is(waitErr, sessionview.ErrCleanup) {
		h.s.viewLeft(waitErr)
	}
	if lost := h.world.Err(); lost != nil {
		h.s.fail(&Error{Kind: ErrWorld, Op: "world", Err: lost})
	}
	// The view has stopped its world; Stop reports how that went.
	if err := h.world.Stop(); err != nil {
		h.s.worldEnded("stop world", err)
	}
	h.s.release(h.lv)
}

// spec builds the view: the closure and home directories, the agent
// host's /etc files and CA directory, the adapter's overlays and masks, the
// shim and the gateway in the view's network namespace.
func (s *session) spec(viewCtx context.Context, world *worldfs.World, opts clirunner.StartOptions, ends *stdio) sessionview.Spec {
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
			Dir: opts.Dir, UID: s.uid, GID: s.uid, Stdin: ends.child[0], Stdout: ends.child[1], Stderr: ends.child[2],
			Grace: opts.KillTimeout},
		Network: sessionview.Network{Setup: func(netns *os.File) error {
			_, err := gateway.Start(viewCtx, gateway.SessionNetwork{Namespace: netns}, s.plan.gateway)
			return err
		}},
		StagingParent: s.dir.entry(stagingEntry),
		CgroupParent:  s.cfg.ViewCgroups,
	}
}

// stdio holds the view process's stdio: the child ends sessionview passes to
// the process and the parent ends the clirunner.Process owns. Without a stdin
// pipe the child's stdin is /dev/null and there is no parent end.
type stdio struct {
	child, parent [3]*os.File
}

func newStdio(needStdin bool) (*stdio, error) {
	e := &stdio{}
	if needStdin {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		e.child[0], e.parent[0] = r, w
	} else {
		null, err := os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		e.child[0] = null
	}
	for i := 1; i < 3; i++ {
		r, w, err := os.Pipe()
		if err != nil {
			e.closeChild()
			e.closeParent()
			return nil, err
		}
		e.child[i], e.parent[i] = w, r
	}
	return e, nil
}

func (e *stdio) stdin() io.WriteCloser {
	if e.parent[0] == nil {
		return nil
	}
	return e.parent[0]
}

func (e *stdio) closeChild()  { closeFiles(e.child[:]) }
func (e *stdio) closeParent() { closeFiles(e.parent[:]) }

func closeFiles(files []*os.File) {
	for _, f := range files {
		if f != nil {
			f.Close()
		}
	}
}
