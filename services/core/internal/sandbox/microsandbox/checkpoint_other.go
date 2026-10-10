//go:build !linux

package microsandbox

import "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"

func CheckpointStore(Config) (string, error) { return "", sandbox.ErrHostUnsupported }
func CheckpointClass(Config) (sandbox.CheckpointCompatibility, error) {
	return sandbox.CheckpointCompatibility{}, sandbox.ErrHostUnsupported
}
