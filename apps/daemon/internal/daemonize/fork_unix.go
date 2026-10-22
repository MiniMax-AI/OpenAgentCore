//go:build unix

package daemonize

import (
	"os/exec"
	"syscall"
)

func configureBackground(cmd *exec.Cmd) (func(), func() error, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return func() {}, func() error { return nil }, nil
}
