//go:build unix

package daemonize

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func configureBackground(cmd *exec.Cmd) (func(), func() error, error) {
	if err := closeInheritedOnExec(); err != nil {
		return nil, nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return func() {}, func() error { return nil }, nil
}

// closeInheritedOnExec keeps descriptors this process inherited, such as the
// native bootstrap's download lock, out of the detached daemon, which outlives
// its caller. Go opens its own descriptors close-on-exec already.
func closeInheritedOnExec() error {
	dir, err := os.Open("/dev/fd")
	if err != nil {
		return fmt.Errorf("daemonize: list open descriptors: %w", err)
	}
	names, err := dir.Readdirnames(-1)
	dir.Close()
	if err != nil {
		return fmt.Errorf("daemonize: list open descriptors: %w", err)
	}
	for _, name := range names {
		if fd, err := strconv.Atoi(name); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
	return nil
}
