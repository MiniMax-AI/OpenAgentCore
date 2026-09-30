//go:build unix

package localworkspace

import (
	"os"
	"syscall"
)

// A file or directory can become a FIFO after its metadata was checked.
// Opening must return before the caller validates the opened descriptor.
func openNativePath(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
