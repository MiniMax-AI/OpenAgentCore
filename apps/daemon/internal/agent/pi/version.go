package pi

import (
	"context"
	"errors"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/versionprobe"
)

// ErrCLIUnavailable reports a missing or unusable Pi CLI.
var ErrCLIUnavailable = errors.New("pi: CLI unavailable")

const defaultBinaryName = "pi"

func defaultBinary() string {
	if value := os.Getenv("OAC_RUNTIME_PI_BIN"); value != "" {
		return value
	}
	return defaultBinaryName
}

// CheckCLIAvailable returns the Pi version, or ErrCLIUnavailable.
func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	return versionprobe.Check(ctx, binary, versionprobe.Config{
		Name:          "pi",
		DefaultBinary: defaultBinary(),
		MissingError:  ErrCLIUnavailable,
	})
}
