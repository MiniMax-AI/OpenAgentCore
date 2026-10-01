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
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/worldfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
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

// launch is ViewSession.Launch: it builds one view and runs opts.Binary in it.
func (s *session) launch(opts clirunner.StartOptions) (*clirunner.Process, error) {
	switch {
	case !slices.Contains(s.plan.view.LocalExec, opts.Binary):
		return nil, &Error{Kind: ErrLaunch, Err: fmt.Errorf("%q is not a LocalExec path", opts.Binary)}
	case !isViewPath(opts.Dir):
		return nil, &Error{Kind: ErrLaunch, Err: fmt.Errorf("directory %q is not absolute and clean", opts.Dir)}
	case !opts.OwnProcessGroup:
		return nil, &Error{Kind: ErrLaunch, Err: errors.New("a view process runs in its own process group")}
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
	if err := s.startBroker(opts.KillTimeout); err != nil {
		s.release(lv)
		return nil, err
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

	// Construction ends with the Session or with the caller.
	startCtx, cancel := context.WithCancel(s.ctx)
	stop := context.AfterFunc(opts.Parent, cancel)
	v, err := sessionview.Start(startCtx, spec)
	stop()
	cancel()
	ends.closeChild()
	if err != nil {
		stopGateway()
		ends.closeParent()
		defer s.release(lv)
		// sessionview stops a world that served; Stop reports how that went.
		if serr := world.Stop(); serr != nil || errors.Is(err, worldfs.ErrAttachmentDirty) {
			return nil, s.worldEnded("launch", errors.Join(err, serr))
		}
		return nil, &Error{Kind: ErrLaunch, Err: err}
	}
	p := v.Presentation()
	s.log.Info("agent host view started", "binary", opts.Binary, "targets", p.Targets, "links", p.Links, "synthesized", p.Synthesized)
	return s.own(lv, v, world, stopGateway, opts, ends)
}

// own hands a started view to the clirunner.Process the adapter receives.
func (s *session) own(lv *liveView, v runningView, world viewWorld, stopGateway func(), opts clirunner.StartOptions, ends *stdio) (*clirunner.Process, error) {
	h := &ownedView{s: s, lv: lv, v: v, world: world, stopGateway: stopGateway, ended: make(chan struct{}), watched: make(chan struct{})}
	go h.watch()
	s.mu.Lock()
	lv.view = v
	closed := lv.closed
	s.mu.Unlock()
	if closed {
		v.Close()
		h.Wait()
		ends.closeParent()
		return nil, &Error{Kind: ErrLaunch, Err: errors.New("the Session is ending")}
	}
	process, err := clirunner.FromHandle(h, clirunner.HandleOptions{Parent: opts.Parent, Stdin: ends.stdin(),
		Stdout: ends.parent[1], Stderr: ends.parent[2], KillTimeout: opts.KillTimeout})
	if err != nil {
		v.Close()
		h.Wait()
		ends.closeParent()
		return nil, &Error{Kind: ErrLaunch, Err: err}
	}
	return process, nil
}

// ownedView is a running view as a clirunner.Handle. Its Wait ends the
// Session's ownership of the view before it returns, so the end the adapter
// observes through the Process comes after it: the gateway has stopped, a
// lost world or one that did not stop cleanly has failed the Session, and
// the view slot is free for the next Launch.
type ownedView struct {
	s           *session
	lv          *liveView
	v           runningView
	world       viewWorld
	stopGateway func()
	ended       chan struct{} // closed once the view has ended
	watched     chan struct{} // closed when watch returns
	once        sync.Once
}

// watch fails the Session as soon as the world is lost while the view runs.
func (h *ownedView) watch() {
	defer close(h.watched)
	select {
	case <-h.world.Lost():
		h.s.fail(&Error{Kind: ErrWorld, Op: "world", Err: h.world.Err()})
	case <-h.ended:
	}
}

func (h *ownedView) Signal(sig syscall.Signal) error { return h.v.Signal(sig) }

func (h *ownedView) Close() error { return h.v.Close() }

func (h *ownedView) Wait() (int, error) {
	exit, err := h.v.Wait()
	h.once.Do(h.end)
	switch {
	case err != nil:
		return -1, err
	case exit.Signal != 0:
		return -1, nil
	}
	return exit.Code, nil
}

// end releases the view once it has ended and its world has stopped.
func (h *ownedView) end() {
	h.stopGateway()
	close(h.ended)
	<-h.watched
	if lost := h.world.Err(); lost != nil {
		h.s.fail(&Error{Kind: ErrWorld, Op: "world", Err: lost})
	}
	// The view has stopped its world; Stop reports how that went.
	if err := h.world.Stop(); err != nil {
		h.s.worldEnded("stop world", err)
	}
	h.s.release(h.lv)
}

// startBroker starts the Session's process broker at its first launch.
func (s *session) startBroker(grace time.Duration) error {
	s.brokerMu.Lock()
	defer s.brokerMu.Unlock()
	if s.broker != nil {
		return nil
	}
	view := s.plan.view
	names := make(map[string]string, len(view.Shims))
	for _, n := range view.Shims {
		names[n] = n
	}
	paths := make(map[string]string, len(view.ShimPaths))
	for _, p := range view.ShimPaths {
		paths[p] = p
	}
	b := s.deps.broker()
	err := b.Start(brokerConfig{UID: s.uid, GID: s.uid, Names: names, Paths: paths,
		Pass: slices.Clone(view.ForwardEnv), Sandbox: s.in.Environment.Sandbox, Tool: s.in.Environment.Tool,
		Dial: s.openProcess, CancelGrace: grace})
	if err != nil {
		err = &Error{Kind: ErrProcessBroker, Err: err}
		s.fail(err)
		return err
	}
	s.broker = b
	return nil
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
