//go:build linux

package processservice

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// launch spawns the process and emits Started or StartFailed, then starts
// the output readers. The leader's exit follows the output it left buffered;
// see markLocked.
func (op *operation) launch(spec sp.ProcessSpec) {
	l, f := op.spawn(spec)
	op.mu.Lock()
	if f != nil {
		op.state, op.startFailure = sp.StateStartFailed, f
		op.scope, op.stdinClosed = sp.ScopeStateClosed, true
		op.push(sp.StartFailedEvent{EventHeader: op.header(), Failure: *f})
		op.settleLocked()
		op.mu.Unlock()
		if op.cu != nil {
			op.cu.close()
		}
		return
	}
	op.pid, op.pty, op.stdin, op.streams, op.openStreams = l.pid, l.pty, l.stdin, l.streams, len(l.streams)
	op.state = sp.StateRunning
	op.push(sp.StartedEvent{EventHeader: op.header()})
	if op.leaderGone {
		op.markLocked()
	}
	cancel, grace := op.cancelPending, op.pendingGrace
	op.mu.Unlock()
	for _, st := range l.streams {
		go op.read(st)
	}
	if cancel {
		op.cancel(grace)
	}
}

// launched is what a successful spawn hands to the operation.
type launched struct {
	pid     int
	pty     *os.File
	stdin   *os.File
	streams []*stream
}

// spawn starts the trampoline in a new session and waits until it has
// exec'd the target or reported why it could not.
func (op *operation) spawn(spec sp.ProcessSpec) (l launched, f *sp.Failure) {
	ioFail := func(what string, err error) *sp.Failure {
		return sp.Fail(sp.CodeIO, sandboxwire.EffectNone, "%s: %v", what, err)
	}
	// Descriptors the child inherits close after the start; the parent's
	// close only when the launch fails. Each is listed as soon as it exists.
	var child, parent []*os.File
	defer func() {
		closeAll(child)
		if f != nil {
			closeAll(parent)
		}
	}()
	pipe := func(what string, childReads bool) (forChild, forParent *os.File, f *sp.Failure) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, ioFail(what, err)
		}
		forChild, forParent = w, r
		if childReads {
			forChild, forParent = r, w
		}
		child, parent = append(child, forChild), append(parent, forParent)
		return forChild, forParent, nil
	}
	files := make([]*os.File, statusFD+1)
	var launchW, statusR *os.File
	if files[launchFD], launchW, f = pipe("launch pipe", true); f != nil {
		return l, f
	}
	if files[statusFD], statusR, f = pipe("status pipe", false); f != nil {
		return l, f
	}
	if spec.PTY != nil {
		master, tty, err := openPTY(*spec.PTY)
		if err != nil {
			return l, ioFail("open terminal", err)
		}
		child, parent = append(child, tty), append(parent, master)
		files[0], files[1], files[2] = tty, tty, tty
		l.pty, l.stdin = master, master
		l.streams = []*stream{{name: sp.StreamTerminal, f: master}}
	} else {
		if files[0], l.stdin, f = pipe("stdin pipe", true); f != nil {
			return l, f
		}
		for i, name := range []sp.Stream{sp.StreamStdout, sp.StreamStderr} {
			var r *os.File
			if files[1+i], r, f = pipe("output pipe", false); f != nil {
				return l, f
			}
			l.streams = append(l.streams, &stream{name: name, f: r})
		}
	}

	attr := &os.ProcAttr{
		Env:   []string{trampolineEnv},
		Files: files,
		// Setctty makes the child's descriptor 0, the terminal, its controlling terminal.
		Sys: &syscall.SysProcAttr{Setsid: true, Setctty: spec.PTY != nil},
	}
	reaping.RLock()
	p, err := os.StartProcess("/proc/self/exe", []string{trampolineArg0}, attr)
	var ferr error
	if err == nil {
		// The reaper cannot reap the child while reaping is held, so its PID
		// still names it.
		var fd int
		if fd, ferr = unix.PidfdOpen(p.Pid, 0); ferr == nil {
			op.cu = newCustody(p.Pid, fd, op.s.stat)
		} else {
			unix.Kill(p.Pid, unix.SIGKILL) // it has not read the launch, so it never execs
		}
		register(p.Pid, op)
	}
	reaping.RUnlock()
	if err != nil {
		return l, ioFail("start trampoline", err)
	}
	pid := p.Pid
	p.Release() // the reaper waits; signals go through killPinned and the custody
	if ferr != nil {
		return l, ioFail("open process descriptor", ferr)
	}
	l.pid = pid
	op.mu.Lock()
	op.pid = l.pid // for killPinned; the rest is published with Started
	op.mu.Unlock()
	closeAll(child)
	child = nil

	_, werr := launchW.Write(encodeLaunch(spec))
	launchW.Close()
	status, rerr := io.ReadAll(statusR)
	statusR.Close()
	switch {
	case len(status) == 6:
		// The trampoline reports only a failure before exec.
		stage, errno := binary.BigEndian.Uint16(status), syscall.Errno(binary.BigEndian.Uint32(status[2:]))
		return l, launchFailure(stage, errno)
	case rerr != nil:
		// Whether exec happened is unknown.
		op.signalScope(unix.SIGKILL)
		return l, sp.Fail(sp.CodeIO, sandboxwire.EffectPossible, "read launch status: %v", rerr)
	case werr != nil || len(status) > 0:
		// The trampoline ended before reading the whole launch or while
		// reporting a failure, so it never exec'd.
		op.killPinned(true, unix.SIGKILL)
		return l, ioFail("launch", errors.Join(werr, errors.New("the trampoline ended before exec")))
	}
	return l, nil
}

func launchFailure(stage uint16, errno syscall.Errno) *sp.Failure {
	what := map[uint16]string{stageLaunch: "read launch", stageDescriptors: "close inherited descriptors", stageChdir: "change directory", stageExec: "exec"}[stage]
	code := sp.CodeIO
	switch errno {
	case unix.ENOENT, unix.ENOTDIR:
		code = sp.CodeNotFound
	case unix.EACCES, unix.EPERM:
		code = sp.CodeUnauthorized
	case unix.ENOEXEC, unix.EINVAL, unix.ELIBBAD, unix.ENAMETOOLONG, unix.ELOOP, unix.EISDIR:
		code = sp.CodeInvalidArgument
	case unix.E2BIG, unix.ENOMEM, unix.EMFILE, unix.ENFILE, unix.EAGAIN:
		code = sp.CodeResourceExhausted
	}
	return sp.Fail(code, sandboxwire.EffectNone, "%s: %v", what, errno)
}

func closeAll(files []*os.File) {
	for _, f := range files {
		f.Close()
	}
}

// openPTY opens a terminal with the requested modes and size. It returns a
// non-blocking master, so closing it interrupts a pending read or write.
func openPTY(spec sp.PTYSpec) (master, tty *os.File, err error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, nil, err
	}
	defer ptmx.Close()
	defer func() {
		if err != nil {
			tty.Close()
		}
	}()
	if err := applyModes(tty, spec.Modes); err != nil {
		return nil, nil, err
	}
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, winsize(spec.Size)); err != nil {
		return nil, nil, err
	}
	fd, err := unix.FcntlInt(ptmx.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, nil, err
	}
	return os.NewFile(uintptr(fd), "/dev/ptmx"), tty, nil
}

// read captures one stream until end of file, a read error, or CloseOutput.
// It reads only while the retained output is below the replay limit, and at
// most the space left, so the streams together never exceed it. Each read
// and the push of what it read happen under op.mu, so every byte is either
// still in the kernel's buffer or in an event; markLocked relies on that.
func (op *operation) read(st *stream) {
	limit := int(op.s.caps.MaxReplayBytesPerOperation)
	buf := make([]byte, op.s.caps.MaxDataBytes)
	var disp sp.OutputDisposition
	rc, err := st.f.SyscallConn()
	for err == nil && disp == 0 {
		op.mu.Lock()
		for op.retained >= limit && !st.abandoned {
			op.cond.Wait()
		}
		op.mu.Unlock()
		// The callback returns false to wait until the stream is readable.
		err = rc.Read(func(fd uintptr) bool {
			op.mu.Lock()
			defer op.mu.Unlock()
			var empty bool
			disp, empty = op.readLocked(st, int(fd), buf)
			return !empty
		})
	}
	st.f.Close()

	op.mu.Lock()
	defer op.mu.Unlock()
	if disp == 0 { // CloseOutput closed the file, or polling it failed
		disp = sp.OutputLost
		if st.abandoned {
			disp = sp.OutputAbandoned
		}
	}
	st.closed = true
	op.push(sp.StreamClosedEvent{EventHeader: op.header(), Stream: st.name, Offset: st.offset, Disposition: disp})
	op.exitWhenDrainedLocked()
	op.worst = max(op.worst, disp)
	if op.openStreams--; op.openStreams > 0 {
		return
	}
	if op.pty != nil {
		op.stdinClosed = true
	}
	worst := op.worst
	op.output = &worst
	op.push(sp.OutputClosedEvent{EventHeader: op.header(), Disposition: worst})
	op.settleLocked()
}

// readLocked reads the non-blocking fd once, unless CloseOutput or the replay
// limit stops it, and pushes what it read. It returns the disposition once
// the stream ends, and empty when nothing is buffered.
func (op *operation) readLocked(st *stream, fd int, buf []byte) (disp sp.OutputDisposition, empty bool) {
	room := int(op.s.caps.MaxReplayBytesPerOperation) - op.retained
	switch {
	case st.abandoned:
		return sp.OutputAbandoned, false
	case room <= 0:
		return 0, false
	}
	buf = buf[:min(len(buf), room)]
	n, err := unix.Read(fd, buf)
	for err == unix.EINTR {
		n, err = unix.Read(fd, buf)
	}
	switch {
	case err == unix.EAGAIN:
		// Nothing is buffered, so whatever the mark counted was read or, on a
		// terminal, discarded by a flush.
		if st.mark > st.offset {
			st.mark = st.offset
			op.exitWhenDrainedLocked()
		}
		return 0, true
	case err == nil && n > 0:
		op.push(sp.OutputEvent{EventHeader: op.header(), Stream: st.name, Offset: st.offset, Data: bytes.Clone(buf[:n])})
		st.offset += uint64(n)
		op.exitWhenDrainedLocked()
		return 0, false
	case err == nil, op.pty != nil && err == unix.EIO: // EIO: every slave descriptor closed
		return sp.OutputDrained, false
	}
	return sp.OutputLost, false
}

// buffered returns the bytes the kernel holds for reading from f: a PTY
// master's input queue, or a pipe's contents (FIONREAD, the same request). It
// returns 0 once f is closed.
func buffered(f *os.File) uint64 {
	n := 0
	if rc, err := f.SyscallConn(); err == nil {
		rc.Control(func(fd uintptr) { n, _ = unix.IoctlGetInt(int(fd), unix.TIOCINQ) })
	}
	return uint64(max(n, 0))
}
