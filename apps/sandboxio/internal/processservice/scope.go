//go:build linux

package processservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// A POSIX session scope is the processes whose session ID is the leader's
// PID. A descendant that calls setsid leaves the scope; that is the scope's
// documented limit.
//
// A session ID is a PID number, so once the session has emptied and its
// leader is reaped, the number can name a new, unrelated session. The service
// signals only processes it can prove are in the operation's session:
//
//   - While the leader is unreaped, its PID pins the session and initial group
//     IDs, and killPinned signals by ID, holding off the reaper.
//   - A custody keeps a pidfd for each process proven to be in the session,
//     starting with the leader's. A process whose stat shows the session ID
//     joins only when a process in custody was in the session both before and
//     after that read: it held the ID throughout, so the ID named this
//     session. Before the reaper reaps a child in custody, the leader
//     included, it refreshes the custody while the child's zombie still holds
//     the ID. A signal goes only to members the same refresh proved still in
//     the session; when a refresh fails, nothing is signaled.
//
// When the custody is empty but a live process still shows the session ID,
// the service cannot tell the session from a new one: the scope becomes
// Unknown and nothing is signaled.
const (
	scopePollFirst = 10 * time.Millisecond
	scopePollMax   = time.Second
)

// errAmbiguous reports a session ID shown by processes none of which can be
// proven to be in the operation's session.
var errAmbiguous = errors.New("no process proven to be in the session remains, but a live process shows its ID")

type procStat struct {
	state   byte
	pgrp    int
	session int
}

// readStat parses /proc/<pid>/stat. The command name may contain spaces and
// parentheses, so fields are counted from the last ')'.
func readStat(pid int) (procStat, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, err
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return procStat{}, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	f := bytes.Fields(b[i+1:])
	if len(f) < 4 || len(f[0]) != 1 {
		return procStat{}, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	pgrp, err1 := strconv.Atoi(string(f[2]))
	session, err2 := strconv.Atoi(string(f[3]))
	if err := errors.Join(err1, err2); err != nil {
		return procStat{}, err
	}
	return procStat{state: f[0][0], pgrp: pgrp, session: session}, nil
}

func (s procStat) live() bool { return s.state != 'Z' && s.state != 'X' }

// gone reports a read of a process that no longer exists.
func gone(err error) bool { return errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) }

// unreaped reports whether fd's process is not yet reaped. EPERM means it
// exists but refuses the caller's signals.
func unreaped(fd int) (bool, error) {
	switch err := unix.PidfdSendSignal(fd, 0, nil, 0); {
	case err == nil, errors.Is(err, unix.EPERM):
		return true, nil
	case errors.Is(err, unix.ESRCH):
		return false, nil
	default:
		return false, fmt.Errorf("pidfd_send_signal: %w", err)
	}
}

// member is a process in custody, with its stat from the last refresh.
type member struct {
	fd int
	st procStat
}

// custody holds the processes proven to be in one operation's session.
type custody struct {
	sid  int
	stat func(pid int) (procStat, error)

	mu      sync.Mutex
	members map[int]member // by PID
	closed  bool
}

// newCustody starts a custody with the leader's pidfd and tracks it for the
// reaper.
func newCustody(sid, leaderFD int, stat func(int) (procStat, error)) *custody {
	c := &custody{sid: sid, stat: stat, members: map[int]member{sid: {fd: leaderFD}}}
	regMu.Lock()
	scopes[c] = struct{}{}
	regMu.Unlock()
	return c
}

// prove reads pid's stat, then checks that fd's process is still unreaped, so
// the stat was that process's. in reports that it was in the session.
func (c *custody) prove(pid, fd int) (st procStat, in bool, err error) {
	st, err = c.stat(pid)
	if gone(err) {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}
	ok, err := unreaped(fd)
	return st, ok && st.session == c.sid, err
}

// pruneLocked drops members that are gone or have left the session and
// returns a member still in it, or 0.
func (c *custody) pruneLocked() (holder int, err error) {
	for pid, m := range c.members {
		st, in, err := c.prove(pid, m.fd)
		if err != nil {
			return 0, err
		}
		if !in {
			unix.Close(m.fd)
			delete(c.members, pid)
			continue
		}
		c.members[pid] = member{fd: m.fd, st: st}
		holder = pid
	}
	return holder, nil
}

// showing lists the processes whose stat shows the session ID.
func (c *custody) showing() (map[int]procStat, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	found := map[int]procStat{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		st, err := c.stat(pid)
		if gone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if st.session == c.sid {
			found[pid] = st
		}
	}
	return found, nil
}

// refreshLocked drops members that are gone or left the session and adds the
// processes proven to have joined it.
func (c *custody) refreshLocked() error {
	for {
		holder, err := c.pruneLocked()
		if err != nil {
			return err
		}
		found, err := c.showing()
		if err != nil {
			return err
		}
		if holder == 0 {
			for _, st := range found {
				if st.live() {
					return errAmbiguous
				}
			}
			return nil
		}
		joined := map[int]member{}
		release := func() {
			for _, m := range joined {
				unix.Close(m.fd)
			}
		}
		for pid := range found {
			if _, ok := c.members[pid]; ok || pid <= 1 {
				continue
			}
			fd, err := unix.PidfdOpen(pid, 0)
			if errors.Is(err, unix.ESRCH) {
				continue
			}
			if err != nil {
				release()
				return fmt.Errorf("pidfd_open: %w", err)
			}
			st, in, err := c.prove(pid, fd)
			if err != nil || !in {
				unix.Close(fd)
				if err != nil {
					release()
					return err
				}
				continue
			}
			joined[pid] = member{fd: fd, st: st}
		}
		// The holder was in the session before the scan. Still in it, it held
		// the session ID throughout, so every process read with that ID was
		// in this session. Otherwise prune drops it and the scan repeats.
		_, in, err := c.prove(holder, c.members[holder].fd)
		if err != nil {
			release()
			return err
		}
		if in {
			for pid, m := range joined {
				c.members[pid] = m
			}
			return nil
		}
		release()
	}
}

// sweep refreshes the custody, then sends sig, unless it is 0, to each live
// member that match accepts. It returns the members signaled and the live
// members; failed joins the delivery failures, after every delivery was tried.
// lost is the refresh failure. A member is signaled only once this refresh
// proved it still in the session, so after a failure, which can leave a
// member that has since left, nothing is signaled.
func (c *custody) sweep(match func(st procStat) bool, sig unix.Signal) (sent, live int, lost, failed error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, 0, nil, nil
	}
	if lost = c.refreshLocked(); lost != nil {
		return 0, 0, lost, nil
	}
	var errs []error
	for pid, m := range c.members {
		if !m.st.live() {
			continue
		}
		live++
		if sig == 0 || !match(m.st) {
			continue
		}
		switch err := unix.PidfdSendSignal(m.fd, sig, nil, 0); {
		case err == nil:
			sent++
		case errors.Is(err, unix.ESRCH):
			live--
		default:
			errs = append(errs, fmt.Errorf("signal process %d: %w", pid, err))
		}
	}
	return sent, live, lost, errors.Join(errs...)
}

// exiting refreshes the custody if pid is a member, while the reaper holds
// its zombie unreaped.
func (c *custody) exiting(pid int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.members[pid]; ok && !c.closed {
		c.refreshLocked() // a failure resurfaces at the next sweep
	}
}

// close releases every pidfd once the scope is closed.
func (c *custody) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for pid, m := range c.members {
		unix.Close(m.fd)
		delete(c.members, pid)
	}
	regMu.Lock()
	delete(scopes, c)
	regMu.Unlock()
}

// killPinned sends sig to the leader, or with group to the initial process
// group, while the leader is unreaped. It holds off the reaper, so the
// leader's PID cannot be reused during the kill. It reports false once the
// leader is reaped or nothing received the signal.
func (op *operation) killPinned(group bool, sig unix.Signal) (bool, error) {
	reaping.RLock()
	defer reaping.RUnlock()
	op.mu.Lock()
	pid, gone := op.pid, op.leaderGone
	op.mu.Unlock()
	if gone || pid <= 1 {
		return false, nil
	}
	if group {
		pid = -pid
	}
	switch err := unix.Kill(pid, sig); {
	case err == nil:
		return true, nil
	case errors.Is(err, unix.ESRCH):
		return false, nil
	default:
		return false, err
	}
}

// signalResult maps a delivery to the Signal response: a failure is IO, with
// EffectPossible once anything was signaled.
func signalResult(sent int, lost, failed error, empty string) error {
	switch {
	case failed != nil || lost != nil && !errors.Is(lost, errAmbiguous):
		effect := sandboxwire.EffectNone
		if sent > 0 {
			effect = sandboxwire.EffectPossible
		}
		return sp.Fail(sp.CodeIO, effect, "signal: %v", errors.Join(lost, failed))
	case sent > 0:
		return nil
	case lost != nil:
		return notRunning("no process can be proven to be in the session")
	}
	return notRunning(empty)
}

// signalGroup signals process group pgrp of the operation's session: the
// initial group by ID while its leader pins it, any other group, or the
// initial group once the leader is reaped, through the custody.
func (op *operation) signalGroup(pgrp int, sig unix.Signal) error {
	if pgrp <= 1 {
		return notRunning("the process group is empty")
	}
	if pgrp == op.sid() {
		if sent, err := op.killPinned(true, sig); sent {
			return nil
		} else if err != nil {
			return sp.Fail(sp.CodeIO, sandboxwire.EffectNone, "signal process group: %v", err)
		}
	}
	sent, _, lost, failed := op.cu.sweep(func(st procStat) bool { return st.pgrp == pgrp }, sig)
	return signalResult(sent, lost, failed, "the process group is empty")
}

// signalScope signals the initial process group by ID while its leader pins
// it, then every other member in custody.
func (op *operation) signalScope(sig unix.Signal) error {
	sid := op.sid()
	grouped, gerr := op.killPinned(true, sig)
	sent, _, lost, failed := op.cu.sweep(func(st procStat) bool { return !grouped || st.pgrp != sid }, sig)
	if grouped {
		sent++
	}
	return signalResult(sent, lost, errors.Join(gerr, failed), "the scope is empty")
}

// watchScope polls until the session is confirmed empty, repeating KILL once
// Cancel's grace has passed so members forked meanwhile die too. A failed
// poll makes the scope Unknown and reports ObservationLost; polling and the
// KILL escalation go on, and the operation settles only once a poll confirms
// the session empty.
func (op *operation) watchScope() {
	delay := scopePollFirst
	for {
		op.mu.Lock()
		sig := unix.Signal(0)
		if op.killing {
			sig = unix.SIGKILL
		}
		op.mu.Unlock()
		_, live, lost, _ := op.cu.sweep(func(procStat) bool { return true }, sig)
		if lost == nil && live == 0 {
			op.cu.close()
			op.scopeClosed()
			return
		}
		op.mu.Lock()
		if lost != nil && op.scope == sp.ScopeStateActive {
			op.scope = sp.ScopeStateUnknown
			op.push(sp.ObservationLostEvent{EventHeader: op.header(), Observation: sp.ObservationScope, Failure: *sp.Fail(sp.CodeIO, sandboxwire.EffectPossible, "observe session: %v", lost)})
		}
		op.mu.Unlock()
		time.Sleep(delay)
		delay = min(2*delay, scopePollMax)
	}
}

// awaitScope waits until the scope has closed or ctx ends. A launch that
// failed closes the scope too.
func (op *operation) awaitScope(ctx context.Context) {
	stop := context.AfterFunc(ctx, func() {
		op.mu.Lock()
		op.cond.Broadcast()
		op.mu.Unlock()
	})
	defer stop()
	op.mu.Lock()
	defer op.mu.Unlock()
	for op.scope != sp.ScopeStateClosed && ctx.Err() == nil {
		op.cond.Wait()
	}
}

func (op *operation) scopeClosed() {
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.killTimer != nil {
		op.killTimer.Stop()
	}
	op.scope = sp.ScopeStateClosed
	op.push(sp.ScopeClosedEvent{EventHeader: op.header()})
	op.settleLocked()
}
