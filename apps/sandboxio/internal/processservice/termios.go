//go:build linux

package processservice

import (
	"os"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

// applyModes sets the requested modes on the terminal.
func applyModes(tty *os.File, modes []sp.PTYModeValue) error {
	fd := int(tty.Fd())
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	sp.ApplyModes(t, modes)
	return unix.IoctlSetTermios(fd, unix.TCSETS, t)
}

func winsize(s sp.WindowSize) *unix.Winsize {
	return &unix.Winsize{Row: s.Rows, Col: s.Cols, Xpixel: s.XPixels, Ypixel: s.YPixels}
}
