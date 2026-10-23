//go:build linux || darwin

package localworkspace

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestNativeUnpublishedWriteStorageFailure(t *testing.T) {
	for _, cause := range []error{syscall.ENOSPC, syscall.EDQUOT} {
		for _, err := range []error{cause, &os.PathError{Op: "write", Path: "private/path", Err: cause}, fmt.Errorf("sync failed: %w", cause), errors.Join(syscall.EIO, cause)} {
			if got := unpublishedWriteError(err); got != ErrWorkspaceWriteUnavailable {
				t.Fatalf("storage exhaustion: got %v, want unavailable", got)
			}
		}
	}
	for _, cause := range []error{syscall.EIO, syscall.EACCES, syscall.EEXIST, syscall.ENOTDIR} {
		if got := unpublishedWriteError(&os.PathError{Op: "write", Path: "private/path", Err: cause}); got != ErrWorkspaceWriteRejected {
			t.Fatalf("changed ordinary rejection: %v", got)
		}
	}
}
