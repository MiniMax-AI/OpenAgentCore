package codex

import (
	"runtime"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
)

// SupportsLocalEnvironment checks deployment prerequisites, not public admission.
func SupportsLocalEnvironment(version string) bool {
	if !SupportsNativeSessionRecovery(version) || (runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows") {
		return false
	}
	binding, err := localworkspace.Load()
	return err == nil && binding != nil
}
