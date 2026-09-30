//go:build darwin

package clirunner

import (
	"errors"
	"golang.org/x/sys/unix"
	"testing"
	"time"
)

func waitExited(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if errors.Is(unix.Kill(pid, 0), unix.ESRCH) || errors.Is(err, unix.ESRCH) || err == nil && (info.Proc.P_pid == 0 || info.Proc.P_stat == 5) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("child %d is still running", pid)
}
