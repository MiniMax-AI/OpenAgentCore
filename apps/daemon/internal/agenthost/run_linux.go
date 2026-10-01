//go:build linux

package agenthost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

const (
	// turnBuffer is how many envelopes a Turn may emit ahead of Output.
	turnBuffer = 64
	// settleBound bounds a cancelled Turn's settlement.
	settleBound = 30 * time.Second
	// executorCloseBound bounds Executor.Close at teardown.
	executorCloseBound = 30 * time.Second
)

// deps are the parts tests replace.
type deps struct {
	dial   dialFunc
	broker func() processBroker
	procs  processTable
}

// Run runs one Session until Input is closed, ctx ends or the Session fails,
// then tears it down. It returns nil after Input closed and every Turn
// settled, ctx's error when ctx ended the Session, and otherwise the error
// that ended it, joined with any view cleanup and teardown failure. A failure
// recorded during teardown counts.
func Run(ctx context.Context, cfg Config, s Session) error {
	return run(ctx, cfg, s, deps{dial: relayDial(cfg), broker: func() processBroker { return unavailableBroker{} }, procs: procfs{}})
}

// Sweep ends every process that holds a uid in cfg.UIDs, then removes every
// Session directory under cfg.StateDir. Run's owner calls it at startup,
// before any Session runs. It returns ErrTeardown when a process still holds
// a Session uid after a bounded wait.
func Sweep(cfg Config) error {
	return sweep(cfg, procfs{}, sweepBound)
}

func sweep(cfg Config, procs processTable, bound time.Duration) error {
	switch {
	case !isHostPath(cfg.StateDir):
		return invalidConfig("state directory %q is not absolute and clean", cfg.StateDir)
	case !cfg.UIDs.valid():
		return invalidConfig("uid range %d+%d", cfg.UIDs.First, cfg.UIDs.Count)
	}
	if err := endProcesses(procs, cfg.UIDs, bound); err != nil {
		return &Error{Kind: ErrTeardown, Op: "sweep processes", Err: err}
	}
	dir := sessionsDir(cfg.StateDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &Error{Kind: ErrTeardown, Op: "sweep", Err: err}
	}
	var errs []error
	for _, e := range entries {
		errs = append(errs, os.RemoveAll(filepath.Join(dir, e.Name())))
	}
	if err := errors.Join(errs...); err != nil {
		return &Error{Kind: ErrTeardown, Op: "sweep", Err: err}
	}
	return nil
}

// session is one running Session.
type session struct {
	cfg  Config
	in   Session
	deps deps
	plan *plan
	link *linkOwner
	log  *slog.Logger
	uid  uint32
	dir  sessionDir

	// ctx ends when the Session fails or tears down.
	ctx    context.Context
	cancel context.CancelFunc

	failMu  sync.Mutex
	failure error   // the first failure, which ended the Session
	cleanup []error // each world that did not stop cleanly

	mu sync.Mutex
	// live is the one view that may run; nil when none does.
	live *liveView
	// views counts launches and their views until each is torn down.
	views sync.WaitGroup

	brokerMu sync.Mutex
	broker   processBroker // started at the first launch
}

func run(ctx context.Context, cfg Config, in Session, d deps) error {
	roots, err := checkConfig(cfg)
	if err != nil {
		return err
	}
	s := &session{cfg: cfg, in: in, deps: d, log: cfg.Log}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	defer s.cancel()
	s.link = newLinkOwner(d.dial, in.Binding, s.fail)
	if s.plan, err = admit(cfg, roots, in, s.openNetwork); err != nil {
		return err
	}
	if s.uid, err = allocUID(cfg.UIDs, d.procs); err != nil {
		return err
	}
	if s.dir, err = createSessionDir(cfg.StateDir, in.Binding.SessionID, s.uid); err != nil {
		freeUID(s.uid)
		return err
	}
	context.AfterFunc(s.ctx, s.closeLive)

	exec, err := s.plan.view.Executor(s.ctx, s.plan.request, agent.ViewSession{
		Home:   agent.ViewDir{Host: s.dir.entry(homeEntry), View: agent.ViewPrivateRoot + "/" + agent.ViewHomeName},
		Proxy:  s.plan.proxy,
		MCP:    s.plan.mcp,
		Launch: s.launch,
	})
	if err != nil {
		err = executorError(err)
	} else {
		err = s.drive(exec)
	}
	return s.finish(exec, err, ctx.Err())
}

// finish tears the Session down and only then decides its result, so a
// failure recorded during teardown counts: the Session's first failure, else
// ended, the end of Run's ctx, else err. Teardown has joined every view and
// stopped the link owner's reports, so nothing changes the result later.
func (s *session) finish(exec agent.Executor, err, ended error) error {
	terr := s.teardown(exec)
	s.failMu.Lock()
	failure, cleanup := s.failure, s.cleanup
	s.failMu.Unlock()
	switch {
	case failure != nil:
		err = failure
	case ended != nil:
		err = ended
	}
	errs := []error{err}
	for _, c := range cleanup {
		if c != failure {
			errs = append(errs, c)
		}
	}
	return errors.Join(append(errs, terr)...)
}

func executorError(err error) error {
	if errors.Is(err, agent.ErrUnsupportedOperation) || errors.Is(err, agent.ErrViewHandoff) {
		return &Error{Kind: ErrUnsupported, Op: "executor", Err: err}
	}
	return &Error{Kind: ErrExecutor, Err: err}
}

// fail records the Session's first failure and ends the Session: its live
// view closes and its Turn is cancelled.
func (s *session) fail(err error) {
	s.failMu.Lock()
	if s.failure == nil {
		s.failure = err
	}
	s.failMu.Unlock()
	s.cancel()
}

// worldEnded records a world that did not stop cleanly, or that cannot show
// that its attachment holds nothing. Only ending the attachment settles its
// state, so the Session fails, and Run reports the error even after another
// failure.
func (s *session) worldEnded(op string, err error) error {
	e := &Error{Kind: ErrWorld, Op: op, Err: err}
	s.failMu.Lock()
	s.cleanup = append(s.cleanup, e)
	s.failMu.Unlock()
	s.fail(e)
	return e
}

// drive runs each Turn from Input in order until Input is closed, a Turn
// fails or the Session ends.
func (s *session) drive(exec agent.Executor) error {
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case in, ok := <-s.in.Input:
			if !ok {
				return nil
			}
			if err := s.turn(exec, in); err != nil {
				return err
			}
		}
	}
}

// turn runs one Turn, forwards its envelopes to Output and waits for its
// settlement. When the Session ends first it cancels the Turn.
func (s *session) turn(exec agent.Executor, in Input) error {
	out := make(chan proto.Envelope, turnBuffer)
	turn, err := exec.StartTurn(s.ctx, in.RunID, in.Message, out)
	if turn == nil {
		if err == nil {
			err = errors.New("no Turn")
		}
		return &Error{Kind: ErrTurn, Op: "start", Err: err}
	}
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for e := range out {
			select {
			case s.in.Output <- e:
			case <-s.ctx.Done():
			}
		}
	}()
	settled, serr := turn.AwaitSettlement(s.ctx)
	if s.ctx.Err() != nil {
		ctx, cancel := context.WithTimeout(context.Background(), settleBound)
		turn.Cancel(ctx)
		settled, serr = turn.AwaitSettlement(ctx)
		cancel()
	}
	if serr != nil {
		return &Error{Kind: ErrTurn, Op: "settle", Err: serr}
	}
	<-forwarded
	switch {
	case err != nil:
		return &Error{Kind: ErrTurn, Op: "start", Err: err}
	case !settled.Reusable:
		return &Error{Kind: ErrTurn, Op: "settle", Err: fmt.Errorf("the Executor is not reusable: %s", settled.Reason)}
	}
	return nil
}

// teardown releases the Session in order: the Executor, the view, the
// process broker, the Link attachment, the Session directory and the uid.
func (s *session) teardown(exec agent.Executor) error {
	var errs []error
	if exec != nil {
		ctx, cancel := context.WithTimeout(context.Background(), executorCloseBound)
		if err := exec.Close(ctx); err != nil {
			errs = append(errs, &Error{Kind: ErrTeardown, Op: "close executor", Err: err})
		}
		cancel()
	}
	// Ending the Session closes the live view and refuses new launches. Under
	// mu, every launch that passed its check has already counted itself.
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	s.views.Wait()
	s.brokerMu.Lock()
	broker := s.broker
	s.brokerMu.Unlock()
	if broker != nil {
		if err := broker.Close(); err != nil {
			errs = append(errs, &Error{Kind: ErrTeardown, Op: "close process broker", Err: err})
		}
	}
	errs = append(errs, s.link.close())
	if err := os.RemoveAll(string(s.dir)); err != nil {
		errs = append(errs, &Error{Kind: ErrTeardown, Op: "remove session directory", Err: err})
	}
	freeUID(s.uid)
	return errors.Join(errs...)
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
