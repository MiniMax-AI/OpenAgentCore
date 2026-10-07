//go:build linux

package processservice

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

type opKey struct {
	attachment sandboxwire.ID
	operation  sandboxwire.ID
}

// operation is one operation record. After Release it is the tombstone:
// digest, state and results stay; events and descriptors are gone.
type operation struct {
	s      *Service
	key    opKey
	digest [sha256.Size]byte

	mu   sync.Mutex
	cond *sync.Cond

	state        sp.OperationState
	exit         *sp.ExitStatus
	startFailure *sp.Failure
	output       *sp.OutputDisposition
	scope        sp.ScopeState
	released     bool
	settled      bool

	// log holds the retained events first..last; retained counts their
	// Output bytes. acked is the highest acknowledged sequence.
	log         []sp.Event
	first, last uint64
	retained    int
	acked       uint64

	// observer is the generation of the stream receiving events, zero when
	// none; delivered is the last sequence handed to it.
	observer, generation uint64
	delivered            uint64

	// The launch publishes these once, with the Started event.
	pid         int
	pty         *os.File // PTY master, nil for pipes
	stdin       *os.File // pipe write end, or the PTY master
	streams     []*stream
	openStreams int

	stdinMu     sync.Mutex
	stdinOffset uint64
	stdinClosed bool
	worst       sp.OutputDisposition

	// leaderGone is set when the reaper reaps the leader, with its status.
	// Exited follows the output buffered then; see markLocked.
	leaderGone bool
	status     unix.WaitStatus
	// cu holds the processes proven to be in the session; the spawn sets it.
	cu *custody

	// A Cancel that arrives while the operation is starting waits for the
	// launch.
	cancelPending bool
	pendingGrace  time.Duration

	killTimer *time.Timer
	killAt    time.Time
	killing   bool
}

type stream struct {
	name      sp.Stream
	f         *os.File
	offset    uint64
	abandoned bool
	closed    bool
	// mark is the offset Exited waits for once the leader is reaped.
	mark uint64
}

func newOperation(s *Service, key opKey, digest [sha256.Size]byte) *operation {
	op := &operation{s: s, key: key, digest: digest, state: sp.StateStarting, scope: sp.ScopeStateActive, first: 1}
	op.cond = sync.NewCond(&op.mu)
	return op
}

func (op *operation) header() sp.EventHeader {
	op.last++
	return sp.EventHeader{OperationID: op.key.operation, Sequence: op.last}
}

// push appends an event whose header came from header() under the same lock.
func (op *operation) push(ev sp.Event) {
	op.log = append(op.log, ev)
	if o, ok := ev.(sp.OutputEvent); ok {
		op.retained += len(o.Data)
	}
	op.cond.Broadcast()
}

func (op *operation) settleLocked() {
	if op.settled {
		return
	}
	switch op.state {
	case sp.StateStartFailed:
	case sp.StateExited, sp.StateUnknown:
		if op.output == nil || op.scope != sp.ScopeStateClosed {
			return
		}
	default:
		return
	}
	op.settled = true
	op.s.active.Add(-1)
	op.cond.Broadcast()
	// No process remains to read pipe stdin.
	if op.pty == nil && op.stdin != nil && !op.stdinClosed {
		op.stdinClosed = true
		op.stdin.Close()
	}
}

func (op *operation) statusLocked() sp.OperationStatus {
	return sp.OperationStatus{
		State:         op.state,
		Exit:          op.exit,
		StartFailure:  op.startFailure,
		StdinOffset:   op.stdinOffset,
		StdinClosed:   op.stdinClosed,
		Output:        op.output,
		Scope:         op.scope,
		Released:      op.released,
		FirstRetained: op.first,
		LastSequence:  op.last,
	}
}

func (op *operation) inspect() sp.OperationStatus {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.statusLocked()
}

func (op *operation) sid() int {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.pid
}

// reaped records the leader's wait status. The reaper calls it.
func (op *operation) reaped(ws unix.WaitStatus) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.leaderGone, op.status = true, ws
	if op.state == sp.StateRunning {
		op.markLocked()
	}
}

// markLocked runs once the leader is reaped and the streams are published. It
// sets each open stream's mark to what it has delivered plus what the kernel
// still buffers for it, so Exited follows every byte written before the reap.
// A read and its push happen under op.mu, so no byte is between the two. Linux
// moves PTY output to the master's input queue, which holds 4 KiB,
// asynchronously through the tty flip buffer; output still there is not
// counted and can follow Exited.
func (op *operation) markLocked() {
	for _, st := range op.streams {
		if !st.closed && !st.abandoned {
			st.mark = st.offset + buffered(st.f)
		}
	}
	op.exitWhenDrainedLocked()
}

// exitWhenDrainedLocked reports the reaped leader's exit once every stream
// has delivered its mark or closed. With less than minRead of room under the
// replay limit no stream is read until the client acknowledges, so the exit
// is reported then: it never waits for an acknowledgement.
func (op *operation) exitWhenDrainedLocked() {
	if !op.leaderGone || op.state != sp.StateRunning {
		return
	}
	if op.room() >= minRead {
		for _, st := range op.streams {
			if !st.closed && !st.abandoned && st.offset < st.mark {
				return
			}
		}
	}
	op.exitedLocked()
}

// room is how much more output the replay limit lets the operation retain.
func (op *operation) room() int {
	return int(op.s.caps.MaxReplayBytesPerOperation) - op.retained
}

// exitedLocked reports the reaped leader's exit and starts watching the scope.
func (op *operation) exitedLocked() {
	ws := op.status
	exit := sp.ExitStatus{Kind: sp.ExitCode, Code: uint8(ws.ExitStatus())}
	if ws.Signaled() {
		exit = sp.ExitStatus{Kind: sp.ExitSignal, Signal: sp.Signal(ws.Signal()), CoreDumped: ws.CoreDump()}
	}
	op.state, op.exit = sp.StateExited, &exit
	op.push(sp.ExitedEvent{EventHeader: op.header(), Status: exit})
	op.settleLocked()
	go op.watchScope()
}

// trimLocked drops acknowledged events, except those the observer has not
// been sent yet: its accepted Attach promised them.
func (op *operation) trimLocked() {
	limit := op.acked
	if op.observer != 0 {
		limit = min(limit, op.delivered)
	}
	if op.first > limit {
		return
	}
	for op.first <= limit {
		if o, ok := op.log[0].(sp.OutputEvent); ok {
			op.retained -= len(o.Data)
		}
		op.log[0] = nil
		op.log = op.log[1:]
		op.first++
	}
	op.cond.Broadcast()
}

// observeLocked makes conn the operation's only observer, from the event
// after sequence after.
func (op *operation) observeLocked(conn *sp.Conn, after uint64) {
	op.generation++
	op.observer, op.delivered = op.generation, after
	op.trimLocked()
	op.cond.Broadcast()
	go op.pump(conn, op.generation)
}

// pump sends retained events to one observer until it is replaced or its
// stream ends. conn.Send blocking on a slow peer is the backpressure; the
// log keeps growing only up to the replay limit, where output reading pauses.
func (op *operation) pump(conn *sp.Conn, gen uint64) {
	drop := func() {
		op.mu.Lock()
		if op.observer == gen {
			op.observer = 0
			op.trimLocked()
		}
		op.cond.Broadcast()
		op.mu.Unlock()
	}
	defer context.AfterFunc(conn.Context(), drop)()
	for {
		op.mu.Lock()
		for op.observer == gen && op.delivered == op.last {
			op.cond.Wait()
		}
		if op.observer != gen {
			op.mu.Unlock()
			return
		}
		op.delivered++
		ev := op.log[op.delivered-op.first]
		op.trimLocked()
		op.mu.Unlock()
		if conn.Send(ev) != nil {
			drop()
			return
		}
	}
}

func (op *operation) attach(conn *sp.Conn, after uint64) (sp.OperationStatus, error) {
	op.mu.Lock()
	defer op.mu.Unlock()
	switch {
	case op.released:
		return sp.OperationStatus{}, released()
	case after > op.last:
		return sp.OperationStatus{}, invalid("sequence %d is after the last event %d", after, op.last)
	case after+1 < op.first:
		return sp.OperationStatus{}, sp.Fail(sp.CodeReplayGap, sandboxwire.EffectNone, "events before %d were acknowledged", op.first)
	}
	op.observeLocked(conn, after)
	return op.statusLocked(), nil
}

func (op *operation) ack(seq uint64) error {
	op.mu.Lock()
	defer op.mu.Unlock()
	switch {
	case op.released:
		return released()
	case seq > op.last:
		return invalid("sequence %d is after the last event %d", seq, op.last)
	}
	op.acked = max(op.acked, seq)
	op.trimLocked()
	return nil
}

func (op *operation) writeStdin(ctx context.Context, offset uint64, data []byte) (uint32, error) {
	op.stdinMu.Lock()
	defer op.stdinMu.Unlock()
	op.mu.Lock()
	switch {
	case op.released:
		op.mu.Unlock()
		return 0, released()
	case op.state == sp.StateStarting:
		op.mu.Unlock()
		return 0, notRunning("the operation is starting")
	case offset != op.stdinOffset:
		op.mu.Unlock()
		return 0, sp.Fail(sp.CodeInputOffsetConflict, sandboxwire.EffectNone, "stdin offset is %d, not %d", op.stdinOffset, offset)
	case op.stdinClosed:
		op.mu.Unlock()
		return 0, sp.Fail(sp.CodeStdinClosed, sandboxwire.EffectNone, "stdin is closed")
	}
	f := op.stdin
	op.mu.Unlock()

	n, err := writeContext(ctx, f, data)
	closed := errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed)
	op.mu.Lock()
	op.stdinOffset += uint64(n)
	if closed && !op.stdinClosed {
		op.stdinClosed = true
		if op.pty == nil {
			f.Close()
		}
	}
	op.mu.Unlock()
	switch {
	case err == nil || n > 0:
		return uint32(n), nil
	case closed:
		return 0, sp.Fail(sp.CodeStdinClosed, sandboxwire.EffectNone, "stdin is closed")
	case errors.Is(err, os.ErrDeadlineExceeded):
		return 0, sp.Fail(sp.CodeCancelled, sandboxwire.EffectNone, "stream ended during the write")
	}
	return 0, sp.Fail(sp.CodeIO, sandboxwire.EffectPossible, "write stdin: %v", err)
}

// writeContext writes to a pollable file, stopping when ctx ends.
func writeContext(ctx context.Context, f *os.File, data []byte) (int, error) {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		f.SetWriteDeadline(time.Now())
		close(done)
	})
	n, err := f.Write(data)
	if !stop() {
		<-done
		f.SetWriteDeadline(time.Time{})
	}
	return n, err
}

func (op *operation) closeStdin(offset uint64) error {
	op.stdinMu.Lock()
	defer op.stdinMu.Unlock()
	op.mu.Lock()
	defer op.mu.Unlock()
	switch {
	case op.released:
		return released()
	case op.state == sp.StateStarting:
		return notRunning("the operation is starting")
	case op.pty != nil:
		return sp.Fail(sp.CodeUnsupported, sandboxwire.EffectNone, "a PTY has no stdin half-close; write the terminal's EOF character")
	case offset != op.stdinOffset:
		return sp.Fail(sp.CodeInputOffsetConflict, sandboxwire.EffectNone, "stdin offset is %d, not %d", op.stdinOffset, offset)
	case op.stdinClosed:
		return nil
	}
	op.stdinClosed = true
	op.stdin.Close()
	return nil
}

func (op *operation) closeOutput(name sp.Stream) error {
	op.mu.Lock()
	st, err := op.abandonLocked(name)
	op.mu.Unlock()
	if err != nil {
		return err
	}
	// Outside op.mu: Close waits for a read in progress, which holds op.mu.
	st.f.Close()
	return nil
}

// abandonOutput closes every open stream as CloseOutput does, once the launch
// has published them.
func (op *operation) abandonOutput() {
	op.mu.Lock()
	for op.state == sp.StateStarting {
		op.cond.Wait()
	}
	streams := op.streams
	op.mu.Unlock()
	for _, st := range streams {
		op.closeOutput(st.name) // a stream already closed needs nothing
	}
}

func (op *operation) abandonLocked(name sp.Stream) (*stream, error) {
	if op.released {
		return nil, released()
	}
	if op.state == sp.StateStarting || op.state == sp.StateStartFailed {
		return nil, notRunning("the operation has no output")
	}
	for _, st := range op.streams {
		if st.name != name {
			continue
		}
		if st.closed || st.abandoned {
			return nil, sp.Fail(sp.CodeOutputClosed, sandboxwire.EffectNone, "stream %d is closed", name)
		}
		st.abandoned = true
		op.cond.Broadcast()
		return st, nil
	}
	return nil, invalid("the operation does not capture stream %d", name)
}

func (op *operation) resize(size sp.WindowSize) error {
	master, err := op.terminal()
	if err != nil {
		return err
	}
	return control(master, func(fd int) error { return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, winsize(size)) })
}

// terminal returns the open PTY master.
func (op *operation) terminal() (*os.File, error) {
	op.mu.Lock()
	defer op.mu.Unlock()
	switch {
	case op.released:
		return nil, released()
	case op.state == sp.StateStarting || op.state == sp.StateStartFailed:
		return nil, notRunning("the operation has no terminal")
	case op.pty == nil:
		return nil, invalid("the operation has no PTY")
	case op.streams[0].closed || op.streams[0].abandoned:
		return nil, sp.Fail(sp.CodeOutputClosed, sandboxwire.EffectNone, "the terminal is closed")
	}
	return op.pty, nil
}

func control(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return sp.Fail(sp.CodeOutputClosed, sandboxwire.EffectNone, "the terminal is closed")
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return sp.Fail(sp.CodeOutputClosed, sandboxwire.EffectNone, "the terminal is closed")
	}
	if ferr != nil {
		return sp.Fail(sp.CodeIO, sandboxwire.EffectNone, "terminal: %v", ferr)
	}
	return nil
}

// signal delivers sig to target. scope.go describes how no signal reaches a
// process outside the operation's session.
func (op *operation) signal(sig sp.Signal, target sp.SignalTarget) error {
	op.mu.Lock()
	switch {
	case op.released:
		op.mu.Unlock()
		return released()
	case op.state == sp.StateStarting || op.state == sp.StateStartFailed:
		op.mu.Unlock()
		return notRunning("the operation has no processes")
	case op.scope == sp.ScopeStateClosed:
		op.mu.Unlock()
		return notRunning("the scope is empty")
	}
	op.mu.Unlock()

	s := unix.Signal(sig)
	switch target {
	case sp.TargetLeader:
		sent, err := op.killPinned(false, s)
		if err != nil {
			return sp.Fail(sp.CodeIO, sandboxwire.EffectNone, "signal leader: %v", err)
		}
		if !sent {
			return notRunning("the leader has exited")
		}
	case sp.TargetInitialProcessGroup:
		return op.signalGroup(op.sid(), s)
	case sp.TargetPTYForegroundGroup:
		master, err := op.terminal()
		if err != nil {
			return err
		}
		var pgrp int
		if err := control(master, func(fd int) (err error) {
			pgrp, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP)
			return err
		}); err != nil || pgrp <= 0 {
			// 0 once the session leader has exited and the terminal lost its session.
			return notRunning("the terminal has no foreground process group")
		}
		return op.signalGroup(pgrp, s)
	case sp.TargetScope:
		return op.signalScope(s)
	}
	return nil
}

// cancel sends TERM to the scope and schedules KILL after grace. An earlier
// deadline from another Cancel stands. A Cancel while the operation is
// starting, including the owner-loss cleanup, takes effect when the launch
// completes.
func (op *operation) cancel(grace time.Duration) error {
	op.mu.Lock()
	switch {
	case op.released:
		op.mu.Unlock()
		return released()
	case op.state == sp.StateStarting:
		if !op.cancelPending || grace < op.pendingGrace {
			op.cancelPending, op.pendingGrace = true, grace
		}
		op.mu.Unlock()
		return nil
	case op.state == sp.StateStartFailed || op.scope == sp.ScopeStateClosed:
		op.mu.Unlock()
		return notRunning("the scope is empty")
	}
	if at := time.Now().Add(grace); op.killTimer == nil || at.Before(op.killAt) {
		if op.killTimer != nil {
			op.killTimer.Stop()
		}
		op.killAt = at
		op.killTimer = time.AfterFunc(grace, op.kill)
	}
	op.mu.Unlock()
	op.signalScope(unix.SIGTERM)
	op.signalScope(unix.SIGCONT) // a stopped process acts on TERM only once continued
	return nil
}

func (op *operation) kill() {
	op.mu.Lock()
	if op.scope == sp.ScopeStateClosed {
		op.mu.Unlock()
		return
	}
	op.killing = true
	op.mu.Unlock()
	op.signalScope(unix.SIGKILL)
}

func (op *operation) release() error {
	op.mu.Lock()
	defer op.mu.Unlock()
	switch {
	case op.released:
		return nil
	case !op.settled:
		return sp.Fail(sp.CodeBusy, sandboxwire.EffectNone, "the operation has not settled")
	}
	op.released = true
	clear(op.log)
	op.log, op.first, op.retained = nil, op.last+1, 0
	op.observer = 0
	op.cond.Broadcast()
	op.pty, op.stdin, op.streams = nil, nil, nil
	return nil
}

func released() error {
	return sp.Fail(sp.CodeReleased, sandboxwire.EffectNone, "the operation was released")
}

func notRunning(reason string) error {
	return sp.Fail(sp.CodeNotRunning, sandboxwire.EffectNone, "%s", reason)
}

func invalid(format string, args ...any) error {
	return sp.Fail(sp.CodeInvalidArgument, sandboxwire.EffectNone, format, args...)
}
