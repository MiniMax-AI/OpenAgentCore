package runtimefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// ValidateLocalPath checks local Windows spelling, not execution authority.
func ValidateLocalPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) || strings.ContainsAny(path, "\x00\r\n*?\"<>|") {
		return ErrUnsafe
	}
	tail := strings.TrimPrefix(path, filepath.VolumeName(path))
	for _, part := range strings.FieldsFunc(tail, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.Contains(part, ":") || strings.TrimRight(part, " .") != part {
			return ErrUnsafe
		}
	}
	return nil
}

func LockDirectory(root *os.Root) (func(), error) {
	// Windows cannot byte-lock a directory. A stable sidecar avoids polluting the
	// capability inventory and is not removed while another process may open it.
	parent, err := os.OpenRoot(filepath.Dir(root.Name()))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	name := "." + filepath.Base(root.Name()) + ".runtime.lock"
	f, err := OpenPrivate(parent, name, os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, err
	}
	overlapped := new(windows.Overlapped)
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped); err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, fmt.Errorf("%w: %w", ErrLocked, err)
		}
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlapped); _ = f.Close() }, nil
}

// Windows has no POSIX directory fsync. The writer flushes the file before
// publication; this does not promise directory durability across power loss.
func SyncDirectory(*os.Root) error { return nil }

func replacePrivate(root *os.Root, from, to string) error {
	// Go's Windows os.Root.Rename uses handle-relative FileRenameInformation with
	// ReplaceIfExists, keeping publication within the held directory.
	return root.Rename(from, to)
}
