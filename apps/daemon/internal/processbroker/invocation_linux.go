//go:build linux

package processbroker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// invocation is one shim request: the operation it started and the
// descriptors the broker pumps for it.
type invocation struct {
	b    *Broker
	conn *processshim.Conn
	log  *slog.Logger
	spec sp.ProcessSpec
	id   sandboxwire.ID
	term *terminal // nil for pipes

	fdMu sync.Mutex
	fds  [3]int // -1 once closed

	// halt ends every wait of the invocation; abort ends every descriptor
	// poll; stopIn ends the stdin pump alone.
	halt     chan struct{}
	haltOnce sync.Once
	abort    *stopFlag
	stopIn   *stopFlag

	writing sync.WaitGroup // the output writers
	helpers sync.WaitGroup // everything else but the stdin pump
	started chan struct{}  // closed once the operation exists or never will

	acks    tracker
	writers map[sp.Stream]*writer
	sigs    chan uint16

	mu       sync.Mutex
	inst     sandboxwire.ID
	cur      handle
	exited   bool // the exit is decided: Exited, StartFailed or exit lost
	shimLost bool
	replied  bool
	// pumping says the stdin pump owns descriptor 0 and stopIn.
	pumping bool
	// settlement, from delivered events
	startFailed, outputClosed, scopeClosed, scopeLost bool
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

// serve reads one request, refuses it or acknowledges it, and runs it.
func (b *Broker) serve(c *processshim.Conn, cred unix.Ucred) {
	log := b.log.With("pid", cred.Pid)
	req, fds, err := c.ReadRequest()
	if err != nil {
		log.Warn("process shim request rejected", "error", err)
		c.Close()
		return
	}
	inv, refusal := b.prepare(req, fds, log)
	if refusal != nil {
		closeAll(fds[:])
		c.Send(*refusal)
		c.Close()
		return
	}
	inv.conn = c
	if err := c.Send(processshim.Ack{}); err != nil {
		inv.halted()
		inv.teardown()
		return
	}
	inv.run()
}

func refuse(code uint8, format string, args ...any) *processshim.Result {
	msg := fmt.Sprintf(format, args...)
	return &processshim.Result{Code: code, Message: []byte(msg[:min(len(msg), processshim.MaxMessageBytes)])}
}

// prepare checks the request and builds the spec. Every refusal happens
// here, before the shim gives up its descriptors.
func (b *Broker) prepare(req processshim.Request, fds [3]int, log *slog.Logger) (*invocation, *processshim.Result) {
	if req.Version != processshim.Version {
		return nil, refuse(processshim.ExitCannotRun, "IPC version %d is not %d", req.Version, processshim.Version)
	}
	remote, ok := b.cfg.Executables.resolve(string(req.ExecPath), string(req.Cwd))
	if !ok {
		return nil, refuse(processshim.ExitNotFound, "%s: not a declared sandbox executable", req.ExecPath)
	}
	if underPrivate(path.Clean(string(req.Cwd))) {
		return nil, refuse(processshim.ExitCannotRun, "%s: the working directory is private to the Session", req.Cwd)
	}
	env, dropped := b.cfg.Environment.compose(req.Env)
	if len(dropped) > 0 {
		log.Info("environment entries naming the private directory dropped", "names", dropped)
	}
	spec := sp.ProcessSpec{
		Executable: []byte(remote),
		Argv:       req.Argv,
		Env:        env,
		Cwd:        req.Cwd,
		Umask:      req.Umask,
		IOMode:     sp.IOPipes,
		Scope:      b.cfg.Scope,
	}
	term, err := openTerminal(fds[0], fds[1])
	if err != nil {
		return nil, refuse(processshim.ExitCannotRun, "terminal: %v", err)
	}
	if term != nil {
		spec.IOMode = sp.IOPTY
		spec.Env = slices.DeleteFunc(spec.Env, func(v sp.EnvVar) bool { return string(v.Name) == "TERM" })
		spec.PTY = &sp.PTYSpec{Size: term.size(), Term: termName(req.Env)}
	}
	if err := spec.Validate(); err != nil {
		term.close()
		return nil, refuse(processshim.ExitCannotRun, "%s: %v", remote, err)
	}
	abort, err1 := newStopFlag()
	stopIn, err2 := newStopFlag()
	if err := errors.Join(err1, err2); err != nil {
		term.close()
		return nil, refuse(processshim.ExitCannotRun, "%v", err)
	}
	inv := &invocation{
		b: b, log: log.With("executable", remote), spec: spec, id: sandboxwire.NewID(), term: term, fds: fds,
		halt: make(chan struct{}), abort: abort, stopIn: stopIn,
		started: make(chan struct{}), sigs: make(chan uint16, 64),
	}
	inv.acks.init()
	inv.writers = map[sp.Stream]*writer{}
	if term != nil {
		inv.writers[sp.StreamTerminal] = &writer{inv: inv, stream: sp.StreamTerminal, fds: []int{1, 2}, wake: make(chan struct{}, 1)}
	} else {
		inv.writers[sp.StreamStdout] = &writer{inv: inv, stream: sp.StreamStdout, fds: []int{1}, wake: make(chan struct{}, 1)}
		inv.writers[sp.StreamStderr] = &writer{inv: inv, stream: sp.StreamStderr, fds: []int{2}, wake: make(chan struct{}, 1)}
	}
	return inv, nil
}

// termName is the remote TERM: the shim's, unless it is unusable.
func termName(environ [][]byte) []byte {
	for _, entry := range environ {
		if v, ok := bytes.CutPrefix(entry, []byte("TERM=")); ok {
			if len(v) == 0 || len(v) > sp.MaxTermBytes || bytes.Contains(v, privateMarker) {
				break
			}
			return v
		}
	}
	return []byte("dumb")
}

func (inv *invocation) run() {
	defer inv.teardown()
	stop := context.AfterFunc(inv.b.ctx, inv.halted)
	defer stop()
	inv.helpers.Add(1)
	go inv.readShim()
	h, caps, ok := inv.start()
	close(inv.started)
	if !ok {
		return
	}
	if inv.term != nil {
		if err := inv.term.makeRaw(); err != nil {
			inv.log.Warn("local terminal stays in its mode", "error", err)
		}
	}
	inv.mu.Lock()
	inv.pumping = true
	inv.mu.Unlock()
	go inv.pumpStdin(caps)
	inv.writing.Add(len(inv.writers))
	for _, w := range inv.writers {
		go w.run()
	}
	inv.helpers.Add(2)
	go inv.ackLoop()
	go inv.forwardSignals()
	inv.observe(h)
}

// start starts the operation. An uncertain Start is retried with the same ID
// and spec on the next stream; a Start that had no effect may move to a new
// service incarnation.
func (inv *invocation) start() (handle, sp.Capabilities, bool) {
	uncertain := false
	for {
		s, err := inv.b.link.get(inv.b.ctx)
		if err != nil {
			inv.fail("the process broker stopped")
			return handle{}, sp.Capabilities{}, false
		}
		if inv.lost() && !uncertain {
			return handle{}, sp.Capabilities{}, false // nothing started
		}
		if inv.inst != s.instance {
			if uncertain {
				inv.fail("the sandbox process service restarted while the program was starting")
				return handle{}, sp.Capabilities{}, false
			}
			inv.inst = s.instance
		}
		if inv.spec.PTY != nil && inv.spec.PTY.Modes == nil {
			inv.spec.PTY.Modes = sp.ReadModes(&inv.term.saved, s.caps.PTYModes)
		}
		if f := s.caps.CheckStart(inv.spec); f != nil {
			inv.reply(processshim.Result{Code: processshim.ExitCannotRun}, fmt.Sprintf("%s: %s", inv.spec.Executable, f.Message))
			return handle{}, sp.Capabilities{}, false
		}
		req := sp.StartRequest{OperationRef: sp.OperationRef{ServerInstanceID: inv.inst, OperationID: inv.id}, Spec: inv.spec}
		if n := len(sp.Encode(req)); n > int(s.caps.MaxStartBytes) {
			inv.reply(processshim.Result{Code: processshim.ExitCannotRun}, fmt.Sprintf("%s: argument list and environment of %d bytes exceed %d", inv.spec.Executable, n, s.caps.MaxStartBytes))
			return handle{}, sp.Capabilities{}, false
		}
		op, _, err := s.client.Start(inv.b.ctx, inv.inst, inv.id, inv.spec)
		if err == nil {
			h := handle{op: op, s: s, relinked: make(chan struct{})}
			inv.mu.Lock()
			inv.cur = h
			inv.mu.Unlock()
			return h, s.caps, true
		}
		f := asFailure(err)
		switch {
		case inv.b.ctx.Err() != nil:
			inv.fail("the process broker stopped")
			return handle{}, sp.Capabilities{}, false
		case !s.ended():
			inv.reply(processshim.Result{Code: processshim.ExitCannotRun}, fmt.Sprintf("%s: %s", inv.spec.Executable, f.Message))
			return handle{}, sp.Capabilities{}, false
		}
		uncertain = uncertain || f.Effect == sandboxwire.EffectPossible
	}
}

// observe handles events until the operation is released, then returns.
func (inv *invocation) observe(h handle) {
	var received uint64
	for {
		select {
		case ev, ok := <-h.op.Events():
			if !ok {
				if inv.isSettled() && inv.releaseOp(h) {
					return
				}
				next, done := inv.reattach(received)
				if done {
					return
				}
				h = next
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
			switch f.Code {
			case sp.CodeReleased:
				return handle{}, true
			case sp.CodeReplayGap:
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
		inv.helpers.Add(1)
		go func() {
			defer inv.helpers.Done()
			// The shim exits only after the output the program wrote before
			// exiting is delivered, as a native exit follows its writes.
			if inv.acks.wait(seq-1, inv.halt) {
				inv.reply(exitResult(ev.Status), "")
				inv.acks.deliver(seq)
			}
		}()
		return false
	case sp.StartFailedEvent:
		inv.decideExit()
		code := uint8(processshim.ExitCannotRun)
		if ev.Failure.Code == sp.CodeNotFound {
			code = processshim.ExitNotFound
		}
		inv.reply(processshim.Result{Code: code}, fmt.Sprintf("%s: %s", inv.spec.Executable, ev.Failure.Message))
		inv.setSettlement(func() { inv.startFailed = true })
	case sp.ObservationLostEvent:
		if ev.Observation == sp.ObservationExit {
			inv.decideExit()
			inv.reply(processshim.Result{Code: processshim.ExitLost}, fmt.Sprintf("the program's exit status was lost: %s", ev.Failure.Message))
		} else {
			inv.setSettlement(func() { inv.scopeLost = true })
		}
	case sp.OutputClosedEvent:
		inv.setSettlement(func() { inv.outputClosed = true })
	case sp.ScopeClosedEvent:
		inv.setSettlement(func() { inv.scopeClosed = true })
	}
	inv.acks.deliver(seq)
	switch {
	case inv.isSettled():
		// Release only after Exited and OutputClosed reached the shim and
		// its descriptors.
		if !inv.acks.wait(seq, inv.halt) {
			h.op.Detach()
			return true
		}
		return inv.releaseOp(h)
	case inv.unsettleable():
		inv.log.Warn("operation scope is unobservable; leaving it to the Session")
		h.op.Detach()
		return true
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

func (inv *invocation) unsettleable() bool {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	return inv.exited && inv.outputClosed && inv.scopeLost
}

// releaseOp releases the settled operation. It reports false when the
// stream ended first, so the caller re-Attaches and releases again.
func (inv *invocation) releaseOp(h handle) bool {
	err := h.op.Release(inv.b.ctx)
	if err == nil {
		return true
	}
	if h.s.ended() && inv.b.ctx.Err() == nil {
		return false
	}
	if f := asFailure(err); f.Code != sp.CodeReleased {
		inv.log.Warn("operation release failed", "error", err)
	}
	h.op.Detach()
	return true
}

func (inv *invocation) decideExit() {
	inv.mu.Lock()
	inv.exited = true
	inv.mu.Unlock()
	inv.stopIn.set()
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

// reply sends the shim its one Result, unless the shim is gone. msg goes to
// the shim's stderr first. The shim then exits, so stdin forwarding has
// stopped and the terminal is restored before the Result is sent.
func (inv *invocation) reply(r processshim.Result, msg string) {
	inv.mu.Lock()
	if inv.replied || inv.shimLost {
		inv.mu.Unlock()
		return
	}
	inv.replied = true
	inv.mu.Unlock()
	inv.stopIn.set()
	if msg != "" {
		inv.writeStderr(msg)
	}
	inv.term.restore()
	inv.conn.Send(r)
	inv.conn.Close()
}

// fail ends the invocation with 255 and the reason on the shim's stderr. The
// reason is written even after the shim exited, because the Harness may still
// read the output the failure cut short.
func (inv *invocation) fail(reason string) {
	inv.log.Warn("process invocation failed", "reason", reason)
	inv.halted()
	inv.writeStderr(reason)
	inv.reply(processshim.Result{Code: processshim.ExitLost}, "")
}

func (inv *invocation) writeStderr(msg string) {
	inv.fdMu.Lock()
	defer inv.fdMu.Unlock()
	if fd := inv.fds[2]; fd >= 0 {
		tryWrite(fd, []byte("oac-process-shim: "+msg+"\n"))
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
	cancel := !inv.exited
	inv.mu.Unlock()
	inv.log.Info("process shim lost", "cancel", cancel)
	inv.stopIn.set()
	inv.term.restore()
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
	for {
		inv.mu.Lock()
		exited := inv.exited
		inv.mu.Unlock()
		if exited {
			return
		}
		err := h.op.Cancel(inv.b.ctx, grace)
		if err == nil {
			return
		}
		if !h.s.ended() {
			if inv.b.ctx.Err() == nil {
				inv.log.Warn("operation cancel failed", "error", err)
			}
			return
		}
		var ok bool
		if h, ok = inv.relink(h); !ok {
			return
		}
	}
}

func (inv *invocation) readShim() {
	defer inv.helpers.Done()
	for {
		m, err := inv.conn.ReadMessage()
		if err != nil {
			inv.shimGone()
			return
		}
		s, ok := m.(processshim.Signal)
		if !ok {
			inv.log.Warn("unexpected message from the process shim")
			inv.conn.Close()
			inv.shimGone()
			return
		}
		select {
		case inv.sigs <- s.Number:
		default:
			inv.log.Warn("signal dropped: too many pending", "signal", s.Number)
		}
	}
}

// halted stops every pump and wait.
func (inv *invocation) halted() {
	inv.haltOnce.Do(func() {
		close(inv.halt)
		inv.abort.set()
		inv.stopIn.set()
	})
}

func (inv *invocation) teardown() {
	inv.halted()
	inv.writing.Wait()
	inv.term.close()
	if inv.conn != nil {
		inv.conn.Close()
	}
	inv.helpers.Wait()
	inv.mu.Lock()
	pumping := inv.pumping
	inv.mu.Unlock()
	if !pumping {
		inv.closeFD(0)
		inv.stopIn.close()
	}
	inv.closeFD(1)
	inv.closeFD(2)
	inv.abort.close()
}

// closeFD closes passed descriptor i once.
func (inv *invocation) closeFD(i int) {
	inv.fdMu.Lock()
	defer inv.fdMu.Unlock()
	if inv.fds[i] >= 0 {
		unix.Close(inv.fds[i])
		inv.fds[i] = -1
	}
}

func closeAll(fds []int) {
	for _, fd := range fds {
		if fd >= 0 {
			unix.Close(fd)
		}
	}
}

func asFailure(err error) *sp.Failure {
	var f *sp.Failure
	if errors.As(err, &f) {
		return f
	}
	return &sp.Failure{Code: sp.CodeUnknown, Effect: sandboxwire.EffectPossible, Message: err.Error()}
}
