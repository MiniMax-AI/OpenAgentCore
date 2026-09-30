//go:build linux

package processservice

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// One loop, Reap, waits for every child of the process; nothing else in the
// process may wait, so no exit status is lost to a competing wait. A launch
// registers its leader's PID before the loop can reap it. Any other child,
// such as an orphan reparented to the binary as a child subreaper, is reaped
// and dropped.
//
// The loop reaps only while holding reaping for writing, so while reaping is
// held for reading an unreaped leader's PID, and the initial process group ID
// equal to it, name the operation's processes. Before reaping a child, the
// loop refreshes every custody holding it (see scope.go).
var (
	reaping sync.RWMutex
	regMu   sync.Mutex
	leaders = map[int]*operation{}
	scopes  = map[*custody]struct{}{}
)

// Reap reaps the process's children until ctx ends and delivers each
// operation leader's exit to its operation. The service binary starts it once,
// before serving, and makes itself a child subreaper so orphaned descendants of
// operations are reaped here too. Operations observe no exit while Reap is not
// running.
func Reap(ctx context.Context) {
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, unix.SIGCHLD)
	defer signal.Stop(sigchld)
	for {
		for reapOne() {
		}
		select {
		case <-sigchld:
		case <-ctx.Done():
			return
		}
	}
}

// childInfo is siginfo_t as waitid fills it for a child.
type childInfo struct {
	signo, errno, code int32
	_                  [unsafe.Sizeof(uintptr(0)) - 4]byte // the union is pointer-aligned
	pid                int32
	_                  [128]byte
}

// reapOne reaps one waitable child and reports whether there was one. It
// peeks at the child without reaping it, refreshes the custodies holding it
// while it still holds its IDs, then waits for exactly that child. That wait
// also consumes a ptrace stop, which Linux reports to a tracing parent even
// without WUNTRACED.
func reapOne() bool {
	reaping.Lock()
	defer reaping.Unlock()
	var info childInfo
	var err error = unix.EINTR
	for err == unix.EINTR {
		err = unix.Waitid(unix.P_ALL, 0, (*unix.Siginfo)(unsafe.Pointer(&info)), unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
	}
	pid := int(info.pid)
	if err != nil || pid <= 0 {
		return false
	}
	regMu.Lock()
	held := make([]*custody, 0, len(scopes))
	for c := range scopes {
		held = append(held, c)
	}
	regMu.Unlock()
	for _, c := range held {
		c.exiting(pid)
	}
	var ws unix.WaitStatus
	got, err := unix.Wait4(pid, &ws, unix.WNOHANG, nil)
	for err == unix.EINTR {
		got, err = unix.Wait4(pid, &ws, unix.WNOHANG, nil)
	}
	if err == nil && got == pid {
		dispatch(pid, ws)
	}
	return true
}

// dispatch hands a child's wait status to its operation. Only an exit ends
// the registration; a stop is not an exit.
func dispatch(pid int, ws unix.WaitStatus) {
	if !ws.Exited() && !ws.Signaled() {
		return
	}
	regMu.Lock()
	op := leaders[pid]
	delete(leaders, pid)
	regMu.Unlock()
	if op != nil {
		op.reaped(ws)
	}
}

// register records a started leader. The caller holds reaping for reading
// from before the fork, so the leader cannot be reaped unregistered.
func register(pid int, op *operation) {
	regMu.Lock()
	leaders[pid] = op
	regMu.Unlock()
}
