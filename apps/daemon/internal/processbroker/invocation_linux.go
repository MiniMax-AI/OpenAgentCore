//go:build linux

package processbroker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// invocation is one shim invocation: the operation it started and the
// streams the broker forwards for it through the relay.
type invocation struct {
	b    *Broker
	rid  uint64 // the relay's invocation ID
	open processshim.Open
	log  *slog.Logger
	spec sp.ProcessSpec
	id   sandboxwire.ID
	term *processshim.Terminal // nil for pipes

	// halt ends every wait of the invocation; stopIn ends stdin forwarding.
	halt     chan struct{}
	haltOnce sync.Once
	stopIn   chan struct{}
	stopOnce sync.Once

	writing sync.WaitGroup // the output writers
	helpers sync.WaitGroup // everything else but the stdin pump
	started chan struct{}  // closed once the operation exists or never will
	// gone ends when the shim is lost.
	gone     context.Context
	loseShim context.CancelFunc

	acks    tracker
	writers map[sp.Stream]*writer
	byFD    [3]*writer
	sigs    chan processshim.Signaled
	// input holds the relay's answer to the outstanding Read.
	input chan processshim.RelayMessage
	// running is set once Started arrives; only observe uses it.
	running bool

	// sendMu orders the invocation's messages before its End.
	sendMu sync.Mutex
	ended  bool

	mu       sync.Mutex
	inst     sandboxwire.ID
	cur      handle
	exited   bool // the exit is decided: Exited, StartFailed or exit lost
	shimLost bool
	replied  bool
	credit   uint32 // the outstanding Read's Max; 0 for none
	// settlement, from delivered events
	startFailed, outputClosed, scopeClosed bool
}

// handle is the operation's handle on one stream. relinked closes when a
// re-Attach replaces it.
type handle struct {
	op       *sp.Operation
	s        *stream
	relinked chan struct{}
}

// ptyGroupSignals target the terminal's foreground group on a PTY.
var ptyGroupSignals = []uint16{
	uint16(unix.SIGINT), uint16(unix.SIGQUIT), uint16(unix.SIGTSTP), uint16(unix.SIGTTIN),
	uint16(unix.SIGTTOU), uint16(unix.SIGCONT), uint16(unix.SIGHUP),
}

func (b *Broker) newInvocation(open processshim.Open) *invocation {
	inv := &invocation{
		b: b, rid: open.ID, open: open, log: b.log.With("invocation", open.ID),
		id: sandboxwire.NewID(), term: open.Terminal,
		halt: make(chan struct{}), stopIn: make(chan struct{}), started: make(chan struct{}),
		sigs: make(chan processshim.Signaled, 64), input: make(chan processshim.RelayMessage, 1),
		writers: map[sp.Stream]*writer{},
	}
	inv.gone, inv.loseShim = context.WithCancel(context.Background())
	inv.acks.init()
	add := func(stream sp.Stream, fd uint8) {
		w := &writer{inv: inv, stream: stream, fd: fd, wake: make(chan struct{}, 1)}
		inv.writers[stream] = w
		inv.byFD[fd] = w
	}
	if inv.term != nil {
		add(sp.StreamTerminal, 1)
	} else {
		add(sp.StreamStdout, 1)
		add(sp.StreamStderr, 2)
	}
	return inv
}

// serve refuses the invocation or accepts and runs it, then ends it.
func (inv *invocation) serve() {
	defer inv.b.wg.Done()
	defer inv.teardown()
	if refusal := inv.prepare(); refusal != nil {
		inv.reply(*refusal, nil)
		return
	}
	stop := context.AfterFunc(inv.b.ctx, inv.halted)
	defer stop()
	if inv.send(processshim.Accept{ID: inv.rid}) != nil {
		return
	}
	inv.run()
}

func refuse(code uint8, format string, args ...any) *processshim.Result {
	return &processshim.Result{Code: code, Message: message(fmt.Sprintf(format, args...))}
}

// message is msg within the IPC's limit.
func message(msg string) []byte {
	if msg == "" {
		msg = "failed"
	}
	return []byte(msg[:min(len(msg), processshim.MaxMessageBytes)])
}

// prepare checks the request and builds the spec. Every refusal happens
// here, before the relay acknowledges the shim.
func (inv *invocation) prepare() *processshim.Result {
	b, req := inv.b, inv.open.Request
	remote, alias, ok := b.cfg.Executables.resolve(string(req.ExecPath), string(req.Cwd))
	if !ok {
		return refuse(processshim.ExitNotFound, "%s: not a declared sandbox executable", req.ExecPath)
	}
	argv, cwd, environ := req.Argv, req.Cwd, req.Env
	if alias != nil {
		argv, cwd, environ = [][]byte{[]byte(alias.Executable)}, []byte(alias.Dir), nil
		for _, a := range alias.Args {
			argv = append(argv, []byte(a))
		}
	} else if underPrivate(path.Clean(string(req.Cwd))) {
		return refuse(processshim.ExitCannotRun, "%s: the working directory is private to the Session", req.Cwd)
	}
	inv.log = inv.log.With("executable", remote)
	env, dropped := b.cfg.Environment.compose(environ)
	if len(dropped) > 0 {
		inv.log.Info("environment entries naming the private directory dropped", "names", dropped)
	}
	spec := sp.ProcessSpec{
		Executable: []byte(remote),
		Argv:       argv,
		Env:        env,
		Cwd:        cwd,
		Umask:      req.Umask,
		IOMode:     sp.IOPipes,
		Scope:      b.cfg.Scope,
	}
	if t := inv.term; t != nil {
		spec.IOMode = sp.IOPTY
		spec.PTY = &sp.PTYSpec{Size: windowSize(t.Size), Term: termName(spec.Env)}
		spec.Env = slices.DeleteFunc(spec.Env, func(v sp.EnvVar) bool { return string(v.Name) == "TERM" })
	}
	if err := spec.Validate(); err != nil {
		return refuse(processshim.ExitCannotRun, "%s: %v", remote, err)
	}
	inv.spec = spec
	return nil
}

func windowSize(s processshim.WindowSize) sp.WindowSize {
	return sp.WindowSize{Rows: s.Rows, Cols: s.Cols, XPixels: s.XPixels, YPixels: s.YPixels}
}

// termios is the terminal's saved mode.
func termios(t *processshim.Terminal) *unix.Termios {
	tio := &unix.Termios{Iflag: t.Iflag, Oflag: t.Oflag, Cflag: t.Cflag, Lflag: t.Lflag}
	copy(tio.Cc[:], t.Cc)
	return tio
}

// termName is the remote TERM: the composed environment's, unless it is
// unusable.
func termName(env []sp.EnvVar) []byte {
	for _, v := range env {
		if string(v.Name) == "TERM" && len(v.Value) > 0 && len(v.Value) <= sp.MaxTermBytes {
			return v.Value
		}
	}
	return []byte("dumb")
}

// run observes the started operation. The program runs, and stdin is
// forwarded, only once its Started event arrives: an operation that a
// retried Start found may still be starting, and refuses stdin until then.
func (inv *invocation) run() {
	h, ok := inv.start()
	close(inv.started)
	if !ok {
		return
	}
	inv.writing.Add(len(inv.writers))
	for _, w := range inv.writers {
		go w.run()
	}
	inv.helpers.Add(2)
	go inv.ackLoop()
	go inv.forwardSignals()
	inv.observe(h)
}

// resolveWindow bounds how long a Start that may have taken effect is
// resolved before the shim gets 255.
const resolveWindow = 30 * time.Second

// startNext says what follows one Start attempt.
type startNext int

const (
	startDone  startNext = iota // the operation is observed
	startEnded                  // the invocation ended
	startNow                    // start again on the next stream
	startLater                  // start again after a backoff
)

// start starts the operation. A Start that may have taken effect is retried
// with the same ID and spec until its outcome is definite: the service then
// answers with the existing operation, or refuses in a way that proves none
// exists. One deadline, set by the first Start that may have taken effect,
// bounds every later link wait, Start, implicit Attach and backoff; when it
// passes, the shim gets 255. A Start that had no effect may move to a new
// service incarnation. Until a Start may have taken effect, losing the shim
// ends the invocation, including a wait for a stream.
func (inv *invocation) start() (handle, bool) {
	var deadline time.Time // set once a Start may have taken effect
	exists := false        // a Start found the operation
	backoff := minBackoff
	for {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			inv.unconfirmed()
			return handle{}, false
		}
		h, next := inv.startOnce(&deadline, &exists)
		switch next {
		case startDone:
			return h, true
		case startEnded:
			return handle{}, false
		case startNow:
			continue
		}
		wait, gone := backoff, inv.gone.Done()
		if !deadline.IsZero() {
			wait, gone = min(wait, time.Until(deadline)), nil
		}
		if !inv.sleepUnless(wait, gone) {
			inv.fail("the process broker stopped")
			return handle{}, false
		}
		backoff = min(2*backoff, maxBackoff)
	}
}

// startOnce makes one Start attempt. A deadline it sets or finds bounds the
// attempt, including the Start's implicit Attach. Until a Start may have
// taken effect, the shim's loss ends the wait for a stream.
func (inv *invocation) startOnce(deadline *time.Time, exists *bool) (handle, startNext) {
	var ctx context.Context
	var cancel context.CancelFunc
	if deadline.IsZero() {
		ctx, cancel = context.WithCancel(inv.b.ctx)
		defer context.AfterFunc(inv.gone, cancel)()
	} else {
		ctx, cancel = context.WithDeadline(inv.b.ctx, *deadline)
	}
	defer cancel()
	s, err := inv.b.link.get(ctx)
	switch {
	case inv.b.ctx.Err() != nil:
		inv.fail("the process broker stopped")
		return handle{}, startEnded
	case deadline.IsZero() && inv.lost():
		return handle{}, startEnded // nothing started
	case err != nil:
		return handle{}, startLater // the deadline passed
	}
	if inv.inst != s.instance {
		if !deadline.IsZero() {
			inv.fail("the sandbox process service restarted while the program was starting")
			return handle{}, startEnded
		}
		inv.inst = s.instance
	}
	if inv.spec.PTY != nil && inv.spec.PTY.Modes == nil {
		inv.spec.PTY.Modes = sp.ReadModes(termios(inv.term), s.caps.PTYModes)
	}
	if f := s.caps.CheckStart(inv.spec); f != nil {
		inv.reply(*refuse(processshim.ExitCannotRun, "%s: %s", inv.spec.Executable, f.Message), nil)
		return handle{}, startEnded
	}
	req := sp.StartRequest{OperationRef: sp.OperationRef{ServerInstanceID: inv.inst, OperationID: inv.id}, Spec: inv.spec}
	if n := len(sp.Encode(req)); n > int(s.caps.MaxStartBytes) {
		inv.reply(*refuse(processshim.ExitCannotRun, "%s: argument list and environment of %d bytes exceed %d", inv.spec.Executable, n, s.caps.MaxStartBytes), nil)
		return handle{}, startEnded
	}
	began := time.Now()
	if deadline.IsZero() {
		var cancelStart context.CancelFunc
		ctx, cancelStart = context.WithDeadline(inv.b.ctx, began.Add(resolveWindow))
		defer cancelStart()
	}
	op, disp, err := s.client.Start(ctx, inv.inst, inv.id, inv.spec)
	if err == nil {
		h := handle{op: op, s: s, relinked: make(chan struct{})}
		inv.mu.Lock()
		inv.cur = h
		inv.mu.Unlock()
		return h, startDone
	}
	f := asFailure(err)
	if inv.b.ctx.Err() != nil {
		inv.fail("the process broker stopped")
		return handle{}, startEnded
	}
	if disp == sp.StartExisting {
		*exists = true // its implicit Attach failed; the next Start attaches again
	}
	if deadline.IsZero() && (*exists || f.Effect == sandboxwire.EffectPossible) {
		*deadline = began.Add(resolveWindow)
	}
	if deadline.IsZero() { // nothing has started
		switch {
		case s.ended():
			return handle{}, startNow
		case f.Code == sp.CodeBusy:
			return handle{}, startLater
		}
		inv.reply(*refuse(processshim.ExitCannotRun, "%s: %s", inv.spec.Executable, f.Message), nil)
		return handle{}, startEnded
	}
	switch {
	case f.Code == sp.CodeInstanceChanged:
		inv.fail("the sandbox process service restarted while the program was starting")
		return handle{}, startEnded
	case f.Code == sp.CodeReleased || f.Code == sp.CodeOperationConflict:
		inv.fail(fmt.Sprintf("the program's start could not be resolved: %s", f.Message))
		return handle{}, startEnded
	case !*exists && f.Effect == sandboxwire.EffectNone && provesAbsence(f.Code):
		inv.reply(*refuse(processshim.ExitCannotRun, "%s: %s", inv.spec.Executable, f.Message), nil)
		return handle{}, startEnded
	}
	return handle{}, startLater
}

// provesAbsence reports whether a Start refusal shows that the ID has no
// operation. A service answers a Start of an existing ID and spec with
// Existing, so a refusal its Start handling makes after that lookup proves
// absence. Busy, InvalidArgument from decoding, and the failures of the
// client and the stream come before the lookup and prove nothing.
func provesAbsence(c sp.ErrorCode) bool {
	switch c {
	case sp.CodeStaleAttachment, sp.CodeResourceExhausted, sp.CodeUnsupported:
		return true
	}
	return false
}

// sleep waits for d and reports false when the invocation halts first.
func (inv *invocation) sleep(d time.Duration) bool { return inv.sleepUnless(d, nil) }

// sleepUnless is sleep that also ends, reporting true, when wake closes.
func (inv *invocation) sleepUnless(d time.Duration, wake <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-wake:
		return true
	case <-inv.halt:
		return false
	}
}

// unconfirmed ends an invocation whose start never became definite, and
// cancels the operation if it exists after all. A Start that takes effect
// later is left to the Session's cleanup.
func (inv *invocation) unconfirmed() {
	inv.fail("the program may have started, but its start could not be confirmed")
	s := inv.b.link.current()
	if s == nil || s.instance != inv.inst {
		return
	}
	ctx, cancel := context.WithTimeout(inv.b.ctx, 5*time.Second)
	defer cancel()
	op, _, err := s.client.Attach(ctx, inv.inst, inv.id, 0)
	if err != nil {
		return
	}
	if err := op.Cancel(ctx, uint32(inv.b.cfg.CancelGrace.Milliseconds())); err != nil {
		inv.log.Warn("operation cancel failed", "error", err)
	}
	op.Detach()
}

// observe handles events until the operation is released, then returns.
func (inv *invocation) observe(h handle) {
	var received uint64
	for {
		select {
		case ev, ok := <-h.op.Events():
			if !ok {
				next, done := inv.reattach(received)
				if done {
					return
				}
				h = next
				// A settled operation has no events left to resume.
				if inv.isSettled() && inv.settle(h, received) {
					return
				}
				continue
			}
			received = ev.Header().Sequence
			if inv.handle(h, ev) {
				return
			}
		case <-inv.halt:
			inv.fail("the process broker stopped")
			h.op.Detach()
			return
		}
	}
}

// reattach resumes the operation after the last received event on a new
// stream. It reports done when the operation is over for the broker.
func (inv *invocation) reattach(after uint64) (handle, bool) {
	backoff := minBackoff
	for {
		s, err := inv.b.link.get(inv.b.ctx)
		if err != nil {
			inv.fail("the process broker stopped")
			return handle{}, true
		}
		if s.instance != inv.inst {
			inv.fail("the sandbox process service restarted; the program's outcome is unknown")
			return handle{}, true
		}
		op, st, err := s.client.Attach(inv.b.ctx, inv.inst, inv.id, after)
		if err != nil {
			if s.ended() {
				continue
			}
			f := asFailure(err)
			switch {
			case refused(f):
				if inv.sleep(backoff) {
					backoff = min(2*backoff, maxBackoff)
					continue
				}
				inv.fail("the process broker stopped")
			case f.Code == sp.CodeReleased:
				return handle{}, true
			case f.Code == sp.CodeReplayGap:
				inv.fail("output was lost while the sandbox was unreachable")
			default:
				inv.fail(fmt.Sprintf("the operation could not be resumed: %s", f.Message))
			}
			return handle{}, true
		}
		if st.Released {
			op.Detach()
			return handle{}, true
		}
		inv.mu.Lock()
		old := inv.cur
		inv.cur = handle{op: op, s: s, relinked: make(chan struct{})}
		h := inv.cur
		inv.mu.Unlock()
		close(old.relinked)
		return h, false
	}
}

// handle applies one event and reports whether the operation is done.
func (inv *invocation) handle(h handle, ev sp.Event) bool {
	seq := ev.Header().Sequence
	switch ev := ev.(type) {
	case sp.StartedEvent:
		if !inv.running {
			inv.running = true
			inv.send(processshim.Started{ID: inv.rid})
			go inv.pumpStdin(h.s.caps)
		}
	case sp.OutputEvent:
		if w := inv.writers[ev.Stream]; w != nil {
			w.push(chunk{seq: seq, data: ev.Data})
			return false
		}
	case sp.StreamClosedEvent:
		if w := inv.writers[ev.Stream]; w != nil {
			w.push(chunk{seq: seq, close: true})
			return false
		}
	case sp.ExitedEvent:
		inv.decideExit()
		// The relay answers the shim once the output the program wrote
		// before exiting is written, as a native exit follows its writes.
		inv.reply(exitResult(ev.Status), inv.marks())
	case sp.StartFailedEvent:
		inv.decideExit()
		code := uint8(processshim.ExitCannotRun)
		if ev.Failure.Code == sp.CodeNotFound {
			code = processshim.ExitNotFound
		}
		inv.reply(*refuse(code, "%s: %s", inv.spec.Executable, ev.Failure.Message), nil)
		inv.setSettlement(func() { inv.startFailed = true })
	case sp.ObservationLostEvent:
		if ev.Observation == sp.ObservationExit {
			inv.decideExit()
			inv.reply(*refuse(processshim.ExitLost, "the program's exit status was lost: %s", ev.Failure.Message), nil)
		} else {
			// The service keeps watching the scope and reports its close.
			inv.log.Info("operation scope observation lost", "reason", ev.Failure.Message)
		}
	case sp.OutputClosedEvent:
		inv.setSettlement(func() { inv.outputClosed = true })
	case sp.ScopeClosedEvent:
		inv.setSettlement(func() { inv.scopeClosed = true })
	}
	inv.acks.deliver(seq)
	if inv.isSettled() {
		return inv.settle(h, seq)
	}
	return false
}

func exitResult(s sp.ExitStatus) processshim.Result {
	if s.Kind == sp.ExitSignal {
		return processshim.Result{Signal: uint16(s.Signal)}
	}
	return processshim.Result{Code: s.Code}
}

func (inv *invocation) setSettlement(set func()) {
	inv.mu.Lock()
	set()
	inv.mu.Unlock()
}

// isSettled mirrors the service's settlement: Release succeeds once it holds.
func (inv *invocation) isSettled() bool {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	return inv.startFailed || (inv.exited && inv.outputClosed && inv.scopeClosed)
}

// settle releases the settled operation once every event through seq
// reached its destination, so Release follows the delivery of Exited and
// OutputClosed. It reports false when the stream ended first; the caller
// re-Attaches and settles again.
func (inv *invocation) settle(h handle, seq uint64) bool {
	if !inv.acks.wait(seq, inv.halt) {
		h.op.Detach()
		return true
	}
	return inv.releaseOp(h)
}

// releaseOp releases the settled operation. A Busy refusal is retried
// until the invocation halts. It reports false when the stream ended first.
func (inv *invocation) releaseOp(h handle) bool {
	for backoff := minBackoff; ; backoff = min(2*backoff, maxBackoff) {
		err := h.op.Release(inv.b.ctx)
		if err == nil {
			return true
		}
		if h.s.ended() && inv.b.ctx.Err() == nil {
			return false
		}
		f := asFailure(err)
		if f.Code == sp.CodeBusy && inv.sleep(backoff) {
			continue
		}
		if f.Code != sp.CodeReleased && f.Code != sp.CodeBusy && inv.b.ctx.Err() == nil {
			inv.log.Warn("operation release failed", "error", err)
		}
		h.op.Detach()
		return true
	}
}

func (inv *invocation) decideExit() {
	inv.mu.Lock()
	inv.exited = true
	inv.mu.Unlock()
	inv.stopInput()
}

func (inv *invocation) lost() bool {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	return inv.shimLost
}

func (inv *invocation) current() handle {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	return inv.cur
}

// relink waits for the handle that replaces h after its stream ended.
func (inv *invocation) relink(h handle) (handle, bool) {
	select {
	case <-h.relinked:
		return inv.current(), true
	case <-inv.halt:
		return h, false
	}
}

// reply has the relay send the shim its one Result once the marked output
// is written, unless the shim has a Result or is gone. It reports false
// then. A Result Message reaches the shim's stderr.
func (inv *invocation) reply(r processshim.Result, marks []processshim.Mark) bool {
	return inv.answer(r, marks, false)
}

// replyBeforeExit is reply that also reports false once the exit is decided.
// It checks the exit and reserves the Result in one step, so the Result
// never replaces a decided exit's.
func (inv *invocation) replyBeforeExit(r processshim.Result) bool {
	return inv.answer(r, nil, true)
}

func (inv *invocation) answer(r processshim.Result, marks []processshim.Mark, beforeExit bool) bool {
	inv.mu.Lock()
	if inv.replied || inv.shimLost || beforeExit && inv.exited {
		inv.mu.Unlock()
		return false
	}
	inv.replied = true
	inv.mu.Unlock()
	inv.stopInput()
	inv.send(processshim.Exit{ID: inv.rid, Result: r, Marks: marks})
	return true
}

// fail ends the invocation with 255 and the reason on the shim's stderr. The
// reason is written even after the shim exited, because the Harness may still
// read the output the failure cut short.
func (inv *invocation) fail(reason string) {
	if inv.b.ctx.Err() == nil { // a stopped broker fails every invocation
		inv.log.Warn("process invocation failed", "reason", reason)
	}
	inv.halted()
	if !inv.reply(processshim.Result{Code: processshim.ExitLost, Message: message(reason)}, nil) {
		inv.send(processshim.Notice{ID: inv.rid, Message: message(reason)})
	}
}

// shimGone handles the end of the shim's connection. Before the exit is
// decided it cancels the operation; afterwards nothing is cancelled, as a
// native background job survives its parent.
func (inv *invocation) shimGone() {
	inv.mu.Lock()
	if inv.replied || inv.shimLost {
		inv.mu.Unlock()
		return
	}
	inv.shimLost = true
	inv.loseShim()
	cancel := !inv.exited
	inv.mu.Unlock()
	inv.log.Debug("process shim lost", "cancel", cancel)
	inv.stopInput()
	if cancel {
		inv.cancelRemote()
	}
}

func (inv *invocation) cancelRemote() {
	select {
	case <-inv.started:
	case <-inv.halt:
		return
	}
	h := inv.current()
	if h.op == nil {
		return
	}
	grace := uint32(inv.b.cfg.CancelGrace.Milliseconds())
	err := inv.request(h, false, func(h handle) error {
		return h.op.Cancel(inv.b.ctx, grace)
	})
	switch {
	case err == nil || inv.b.ctx.Err() != nil:
	case asFailure(err).Effect == sandboxwire.EffectPossible:
		// A second Cancel would send the scope TERM again; the events tell
		// whether this one took effect.
		inv.log.Info("operation cancel outcome unknown; not sent again", "error", err)
	default:
		inv.log.Warn("operation cancel failed", "error", err)
	}
}

// request makes req on the operation until it succeeds or fails for good,
// and returns the last failure. A Busy refusal is repeated after a backoff
// until the invocation halts. When the stream ends, req is repeated on the
// next stream if it had no effect, or if it is idempotent.
func (inv *invocation) request(h handle, idempotent bool, req func(handle) error) error {
	backoff := minBackoff
	for {
		err := req(h)
		if err == nil {
			return nil
		}
		f := asFailure(err)
		switch {
		case h.s.ended() && (idempotent || f.Effect == sandboxwire.EffectNone):
			var ok bool
			if h, ok = inv.relink(h); !ok {
				return err
			}
		case refused(f):
			if !inv.sleep(backoff) {
				return err
			}
			backoff = min(2*backoff, maxBackoff)
		default:
			return err
		}
	}
}

// refused reports whether the service refused a request with Busy, without
// effect; the same request may follow.
func refused(f *sp.Failure) bool {
	return f.Code == sp.CodeBusy && f.Effect == sandboxwire.EffectNone
}

// receive takes one relay message for the invocation. It never blocks, and
// returns an error for a message that breaks the IPC.
func (inv *invocation) receive(m processshim.RelayMessage) error {
	switch m := m.(type) {
	case processshim.Input:
		return inv.answered(m, len(m.Data))
	case processshim.InputEnd:
		return inv.answered(m, 0)
	case processshim.Written:
		w := inv.byFD[m.FD]
		if w == nil {
			return fmt.Errorf("%w: fd %d has no output", processshim.ErrProtocol, m.FD)
		}
		if err := w.written(m.Seq); err != nil {
			return err
		}
		inv.acks.deliver(m.Seq)
	case processshim.WriteFailed:
		w := inv.byFD[m.FD]
		if w == nil {
			return fmt.Errorf("%w: fd %d has no output", processshim.ErrProtocol, m.FD)
		}
		sent, err := w.failed(m.Seq)
		if err != nil {
			return err
		}
		// The reader is gone; the remote writer gets EPIPE as it would
		// locally. The output is delivered at once: closing the remote
		// output may wait for a new stream, which the operation's
		// settlement may in turn wait behind.
		for _, seq := range sent {
			inv.acks.deliver(seq)
		}
		// A reader that closes its end, as on a Cancel, is expected.
		if errno := unix.Errno(m.Errno); errno == unix.EPIPE || errno == unix.ECONNRESET {
			inv.log.Debug("output reader gone", "stream", w.stream, "error", errno)
		} else {
			inv.log.Info("output write failed", "stream", w.stream, "error", errno)
		}
		inv.helpers.Add(1)
		go func() {
			defer inv.helpers.Done()
			inv.closeOutput(w.stream)
		}()
	case processshim.Signaled:
		select {
		case inv.sigs <- m:
		default:
			inv.log.Warn("signal dropped: too many pending", "signal", m.Number)
		}
	case processshim.Gone:
		inv.helpers.Add(1)
		go func() {
			defer inv.helpers.Done()
			inv.shimGone()
		}()
	}
	return nil
}

// answered takes the relay's answer to the outstanding Read.
func (inv *invocation) answered(m processshim.RelayMessage, n int) error {
	inv.mu.Lock()
	credit := inv.credit
	inv.credit = 0
	inv.mu.Unlock()
	if credit == 0 || n > int(credit) {
		return fmt.Errorf("%w: %d bytes of stdin without a Read for them", processshim.ErrProtocol, n)
	}
	select {
	case inv.input <- m:
	default: // the stdin pump took the previous answer before granting
	}
	return nil
}

// send sends m unless the invocation has ended.
func (inv *invocation) send(m processshim.BrokerMessage) error {
	inv.sendMu.Lock()
	defer inv.sendMu.Unlock()
	if inv.ended {
		return errEnded
	}
	return inv.b.send(m)
}

// halting reports whether the invocation halted.
func (inv *invocation) halting() bool {
	select {
	case <-inv.halt:
		return true
	default:
		return false
	}
}

// halted stops every pump and wait.
func (inv *invocation) halted() {
	inv.haltOnce.Do(func() {
		close(inv.halt)
		inv.stopInput()
	})
}

// stopInput ends stdin forwarding.
func (inv *invocation) stopInput() {
	inv.stopOnce.Do(func() {
		close(inv.stopIn)
		inv.send(processshim.StopInput{ID: inv.rid})
	})
}

// teardown ends the invocation: once no relay message reaches it and its
// helpers are done, End tells the relay to close its descriptors. The stdin
// pump is not waited for, so teardown never waits for a stdin request still
// on the stream; it sends nothing after End.
func (inv *invocation) teardown() {
	inv.halted()
	inv.b.unregister(inv.rid)
	inv.writing.Wait()
	inv.helpers.Wait()
	inv.sendMu.Lock()
	defer inv.sendMu.Unlock()
	inv.ended = true
	inv.b.send(processshim.End{ID: inv.rid})
}

func asFailure(err error) *sp.Failure {
	var f *sp.Failure
	if errors.As(err, &f) {
		return f
	}
	return &sp.Failure{Code: sp.CodeUnknown, Effect: sandboxwire.EffectPossible, Message: err.Error()}
}
