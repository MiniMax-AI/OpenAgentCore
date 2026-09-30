//go:build linux || darwin

package cli

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func nativeAvailableSpace(directory string) (uint64, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(directory, &s); err != nil {
		return 0, err
	}
	return uint64(s.Bavail) * uint64(s.Bsize), nil
}
func nativeDiskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}
func nativeTerminal(f *os.File) bool {
	_, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	return err == nil
}
