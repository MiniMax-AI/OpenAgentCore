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
// the output readers. The reaper reports the leader's exit.
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
		op.exitedLocked()
	}
	cancel, grace := op.cancelPending, op.pendingGrace
	op.mu.Unlock()
	// Each stream reads at most its share of the replay limit, so a reader
	// blocked on an idle stream never holds the space another one needs.
	chunk := min(int(op.s.caps.MaxDataBytes), int(op.s.caps.MaxReplayBytesPerOperation)/len(l.streams))
	for _, st := range l.streams {
		go op.read(st, chunk)
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
// Before each read of up to chunk bytes it reserves chunk bytes of the replay
// limit, so the streams together never exceed it, and it pauses until they
// are free.
func (op *operation) read(st *stream, chunk int) {
	limit := int(op.s.caps.MaxReplayBytesPerOperation)
	buf := make([]byte, chunk)
	var disp sp.OutputDisposition
	for disp == 0 {
		op.mu.Lock()
		for op.retained+op.reserved+chunk > limit && !st.abandoned {
			op.cond.Wait()
		}
		if st.abandoned {
			op.mu.Unlock()
			disp = sp.OutputAbandoned
			break
		}
		op.reserved += chunk
		op.mu.Unlock()
		n, err := st.f.Read(buf)
		op.mu.Lock()
		op.reserved -= chunk
		if n > 0 {
			op.push(sp.OutputEvent{EventHeader: op.header(), Stream: st.name, Offset: st.offset, Data: bytes.Clone(buf[:n])})
			st.offset += uint64(n)
		} else {
			op.cond.Broadcast() // the unused reservation is free again
		}
		switch {
		case err == nil:
		case st.abandoned:
			disp = sp.OutputAbandoned
		case err == io.EOF, op.pty != nil && errors.Is(err, syscall.EIO): // EIO: every slave descriptor closed
			disp = sp.OutputDrained
		default:
			disp = sp.OutputLost
		}
		op.mu.Unlock()
	}
	st.f.Close()

	op.mu.Lock()
	defer op.mu.Unlock()
	st.closed = true
	op.push(sp.StreamClosedEvent{EventHeader: op.header(), Stream: st.name, Offset: st.offset, Disposition: disp})
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
