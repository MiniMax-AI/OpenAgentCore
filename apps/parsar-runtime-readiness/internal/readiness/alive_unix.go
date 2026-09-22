//go:build unix

package readiness

import (
	"syscall"
)

// alive reports whether pid is running: the POSIX "signal 0" probe, which is
// what a sandbox provides.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}
