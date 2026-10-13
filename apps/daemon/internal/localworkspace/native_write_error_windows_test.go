package localworkspace

import (
	"os"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"golang.org/x/sys/windows"
)

func TestNativeUnpublishedWriteStorageFailure(t *testing.T) {
	for _, cause := range []error{windows.ERROR_DISK_FULL, windows.ERROR_HANDLE_DISK_FULL, windows.ERROR_DISK_QUOTA_EXCEEDED} {
		if got := unpublishedWriteError(&os.PathError{Op: "write", Path: "private/path", Err: cause}); got != agent.ErrWorkspaceWriteUnavailable {
			t.Fatalf("storage exhaustion: got %v, want unavailable", got)
		}
	}
	if got := unpublishedWriteError(windows.ERROR_ACCESS_DENIED); got != agent.ErrWorkspaceWriteRejected {
		t.Fatalf("changed ordinary rejection: %v", got)
	}
}
