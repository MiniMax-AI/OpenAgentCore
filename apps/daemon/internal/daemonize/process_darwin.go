//go:build darwin

package daemonize

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func identifyProcess(pid int) (processIdentity, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) || (err == nil && (info.Proc.P_pid == 0 || info.Proc.P_stat == 5)) {
		return processIdentity{}, ErrNotRunning
	}
	if err != nil {
		if errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
			return processIdentity{}, ErrNotRunning
		}
		return processIdentity{}, err
	}
	return processIdentity{PID: pid, Start: fmt.Sprintf("%d:%d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec)}, nil
}

func stopProcess(identity processIdentity, timeout time.Duration) error {
	current, err := identifyProcess(identity.PID)
	if errors.Is(err, ErrNotRunning) {
		return nil
	}
	if err != nil || current.Start != identity.Start {
		return ErrStaleOrCorrupt
	}
	// Darwin has no pidfd; identity is checked immediately before signalling.
	// PID reuse between this check and kill remains an OS-level race.
	if err = unix.Kill(identity.PID, unix.SIGTERM); err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		current, err = identifyProcess(identity.PID)
		if errors.Is(err, ErrNotRunning) || (err == nil && current.Start != identity.Start) {
			return nil
		}
		if err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("daemonize: cleanup unconfirmed; process record retained")
}
