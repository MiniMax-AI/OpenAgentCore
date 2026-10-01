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
	// nativeBound bounds a Turn's settlement and each Executor.Close, as
	// dispatch's preparedCancelTimeout does.
	nativeBound = 10 * time.Second
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

// Sweep ends every process that holds a uid in cfg.UIDs, with its view's PID
// namespace, then removes every Session directory under cfg.StateDir. Run's
// owner calls it at startup, before any Session runs, with /proc showing the
// agent host's own PID namespace. It returns ErrTeardown when a task still
// holds a Session uid after a bounded wait.
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

	// The goroutine that runs drive and then teardown owns these.
	fwd        *forwarder // the last Turn's forwarder
	execClosed bool       // a Close of the Executor succeeded
	execErr    error      // the last Close's error
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

// The Turn driving below mirrors the daemon's prepared execution in
// apps/daemon/internal/dispatch: startPreparedExecution
// (preparation_start.go), forwardPreparedOutput, runPreparedRelease and
// forwardPreparedTerminal (prepared_handoff.go), as harness-onboarding.md's
// "What the Runtime does around a Turn" describes them. A Session has no
// steering, functions or interactions, so no admitted operation joins the
// release; the end of the Session stands in for the connection's shutdown.

// forwarder is a Turn's one output consumer. It starts before StartTurn and
// drains out: it forwards each envelope to Output in order until the Session
// ends, and keeps the Turn's Done for turn to publish after settlement.
type forwarder struct {
	runID string
	out   chan proto.Envelope
	// ended closes at the Turn's terminal observation: its Done, a protocol
	// error or the close of out.
	ended chan struct{}
	// abort closes when the Turn is to be cancelled: a protocol error, a
	// failed start or the end of the Session.
	abort chan struct{}
	// stop makes the forwarder return without draining further.
	stop chan struct{}
	// done closes when the forwarder has returned and sends nothing more.
	done chan struct{}

	endOnce, abortOnce, stopOnce sync.Once

	mu          sync.Mutex
	terminal    *proto.Envelope
	protocolErr error
}

func newForwarder(runID string) *forwarder {
	return &forwarder{runID: runID, out: make(chan proto.Envelope, turnBuffer),
		ended: make(chan struct{}), abort: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
}

func (f *forwarder) end()    { f.endOnce.Do(func() { close(f.ended) }) }
func (f *forwarder) cancel() { f.abortOnce.Do(func() { close(f.abort) }) }
func (f *forwarder) halt()   { f.stopOnce.Do(func() { close(f.stop) }) }

func (f *forwarder) aborted() bool {
	select {
	case <-f.abort:
		return true
	default:
		return false
	}
}

// forward runs f until out closes or f is halted.
func (s *session) forward(f *forwarder) {
	defer close(f.done)
	for {
		select {
		case <-f.stop:
			return
		case e, ok := <-f.out:
			if !ok {
				f.end()
				return
			}
			f.mu.Lock()
			if e.ID != f.runID || f.terminal != nil {
				if f.protocolErr == nil {
					f.protocolErr = errors.New("executor output crossed the Turn boundary")
				}
				f.mu.Unlock()
				f.end()
				f.cancel()
				continue
			}
			if e.Type == proto.TypeDone {
				f.terminal = &e
				f.mu.Unlock()
				f.end()
				continue
			}
			f.mu.Unlock()
			if s.ctx.Err() == nil {
				select {
				case s.in.Output <- e:
				case <-s.ctx.Done():
				}
			}
		}
	}
}

// turn runs one Turn. Its forwarder starts before StartTurn. Once the Turn's
// output ends, or the Turn is to be cancelled, turn awaits its settlement,
// closes the Executor when the Turn leaves it unusable and only then
// publishes the Turn's Done, after an Error envelope when the Turn failed.
// When Close fails, the Executor keeps the Turn and nothing is published.
// When the Session has ended, nothing is published and turn returns nil.
func (s *session) turn(exec agent.Executor, in Input) error {
	f := newForwarder(in.RunID)
	s.fwd = f
	go s.forward(f)
	defer context.AfterFunc(s.ctx, f.cancel)()
	turn, startErr := exec.StartTurn(s.ctx, in.RunID, in.Message, f.out)
	if turn == nil {
		// out stays with the caller. The failed Turn ends the Session, and
		// teardown closes the Executor.
		close(f.out)
		<-f.done
		if startErr == nil {
			startErr = errors.New("no Turn")
		}
		return &Error{Kind: ErrTurn, Op: "start", Err: startErr}
	}
	if startErr != nil {
		f.cancel()
	}
	select {
	case <-f.ended:
	case <-f.abort:
	}
	settlement, nativeErr := settle(turn, f.abort)
	if nativeErr == nil {
		// Settlement confirms that out is closed.
		select {
		case <-f.done:
		case <-s.ctx.Done():
		}
	}
	f.mu.Lock()
	terminal, protocolErr := f.terminal, f.protocolErr
	if terminal == nil && protocolErr == nil && !f.aborted() {
		protocolErr = errors.New("executor output ended without a terminal result")
	}
	f.mu.Unlock()
	if nativeErr != nil || !settlement.Reusable || startErr != nil || protocolErr != nil || s.ctx.Err() != nil {
		if err := s.closeExecutor(exec); err != nil {
			return &Error{Kind: ErrTurn, Op: "close executor", Err: errors.Join(nativeErr, err)}
		}
	}
	// A confirmed Close confirms that out is closed too.
	select {
	case <-f.done:
	case <-s.ctx.Done():
		return nil
	}

	var failure string
	var result error
	switch {
	case nativeErr != nil:
		failure, result = "executor Turn settlement failed", &Error{Kind: ErrTurn, Op: "settle", Err: nativeErr}
	case protocolErr != nil:
		failure, result = protocolErr.Error(), &Error{Kind: ErrTurn, Op: "output", Err: protocolErr}
	case startErr != nil:
		failure, result = "executor Turn could not start", &Error{Kind: ErrTurn, Op: "start", Err: startErr}
	case !settlement.Reusable:
		result = &Error{Kind: ErrTurn, Op: "settle", Err: fmt.Errorf("the Executor is not reusable: %s", settlement.Reason)}
	}
	if failure != "" {
		e, err := proto.NewEnvelope(proto.TypeError, in.RunID, proto.ErrorPayload{Error: failure})
		if err != nil {
			return errors.Join(result, err)
		}
		s.publish(e)
	}
	if terminal == nil {
		e, err := proto.NewEnvelope(proto.TypeDone, in.RunID, proto.DonePayload{})
		if err != nil {
			return errors.Join(result, err)
		}
		terminal = &e
	}
	s.publish(*terminal)
	if s.ctx.Err() != nil {
		return nil
	}
	return result
}

// settle awaits turn's settlement for at most nativeBound. When abort closes
// first it cancels the Turn, and a failed Cancel ends the wait; natural
// completion never calls Cancel.
func settle(turn agent.Turn, abort <-chan struct{}) (agent.TurnSettlement, error) {
	ctx, cancel := context.WithTimeout(context.Background(), nativeBound)
	defer cancel()
	cancelled := make(chan error, 1)
	settled := make(chan struct{})
	go func() {
		select {
		case <-abort:
			err := turn.Cancel(ctx)
			if err != nil {
				cancel()
			}
			cancelled <- err
		case <-settled:
			cancelled <- nil
		}
	}()
	settlement, err := turn.AwaitSettlement(ctx)
	close(settled)
	return settlement, errors.Join(err, <-cancelled)
}

// publish sends e to Output while the Session runs.
func (s *session) publish(e proto.Envelope) {
	if s.ctx.Err() != nil {
		return
	}
	select {
	case s.in.Output <- e:
	case <-s.ctx.Done():
	}
}

// closeExecutor closes exec for at most nativeBound, until a Close succeeds.
// A failed Close retains the Executor's resources, and a later call retries
// it.
func (s *session) closeExecutor(exec agent.Executor) error {
	if exec == nil || s.execClosed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeBound)
	defer cancel()
	s.execErr = exec.Close(ctx)
	s.execClosed = s.execErr == nil
	return s.execErr
}

// teardown releases the Session in order: the Executor, the view, the
// process broker, the Link attachment, the Session directory and the uid.
// When Close fails, teardown ends the views, which kills each view's
// processes, and retries Close once. If that fails too, the Executor may
// still use the Session directory: teardown returns ErrTeardown and keeps
// the directory and the uid, which stays in use until the agent host exits;
// the next agent host's Sweep reclaims both.
func (s *session) teardown(exec agent.Executor) error {
	var errs []error
	closeErr := s.execErr
	if closeErr == nil {
		closeErr = s.closeExecutor(exec)
	}
	// Ending the Session closes the live view and refuses new launches. Under
	// mu, every launch that passed its check has already counted itself.
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	s.views.Wait()
	if closeErr != nil {
		closeErr = s.closeExecutor(exec)
	}
	// The Session has ended, so the forwarder sends nothing more.
	if f := s.fwd; f != nil {
		f.halt()
		<-f.done
	}
	s.brokerMu.Lock()
	broker := s.broker
	s.brokerMu.Unlock()
	if broker != nil {
		if err := broker.Close(); err != nil {
			errs = append(errs, &Error{Kind: ErrTeardown, Op: "close process broker", Err: err})
		}
	}
	errs = append(errs, s.link.close())
	if closeErr != nil {
		errs = append(errs, &Error{Kind: ErrTeardown, Op: "close executor", Err: closeErr})
		return errors.Join(errs...)
	}
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
