//go:build !linux

package processshim

import (
	"errors"
	"fmt"
	"os"
)

// ErrUnsupported is returned on platforms without the process shim.
var ErrUnsupported = errors.New("processshim: unsupported on this platform")

// Run reports ErrUnsupported and returns ExitCannotRun.
func Run(string) int {
	fmt.Fprintf(os.Stderr, "oac-process-shim: %v\n", ErrUnsupported)
	return ExitCannotRun
}

// Relaying reports false.
func Relaying() bool { return false }

// Relay reports ErrUnsupported and returns 1.
func Relay() int {
	fmt.Fprintf(os.Stderr, "oac-process-shim: %v\n", ErrUnsupported)
	return 1
}
