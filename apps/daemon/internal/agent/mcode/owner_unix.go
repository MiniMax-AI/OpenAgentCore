//go:build unix

package mcode

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// runAsOwner runs command as the user and group that own dir, with no
// supplementary groups. An agent-host view gives the Session home to the
// Session user, and the daemon reads it as that user.
func runAsOwner(command *exec.Cmd, dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return fmt.Errorf("mcode: Session home is not a directory")
	}
	if int(owner.Uid) == os.Geteuid() && int(owner.Gid) == os.Getegid() {
		return nil
	}
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: owner.Uid, Gid: owner.Gid, Groups: []uint32{}}}
	return nil
}
