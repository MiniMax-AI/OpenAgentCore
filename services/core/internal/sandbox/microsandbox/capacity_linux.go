//go:build linux

package microsandbox

import (
	"fmt"
	"runtime"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func hostCapacity(r sandbox.Resources) error {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return fmt.Errorf("cannot verify node memory capacity")
	}
	return sandbox.CheckCapacity(r, runtime.NumCPU(), uint64(info.Totalram)*uint64(info.Unit))
}
