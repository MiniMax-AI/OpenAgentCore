package mcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/versionprobe"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

var ErrCLINotFound = errors.New("mcode CLI not found")

var SupportedVersion = declaration.Declaration.Native.Version

// binaryEnv pins the mcode executable in images where PATH is not under our
// control, so the version probe and the run-time spawn resolve the same binary.
const binaryEnv = "OAC_RUNTIME_MCODE_BIN"

// defaultBinary is the trimmed binaryEnv override, else "mcode" on PATH. Read at call time.
func defaultBinary() string {
	if v := strings.TrimSpace(os.Getenv(binaryEnv)); v != "" {
		return v
	}
	return "mcode"
}

func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	version, err := versionprobe.Check(ctx, binary, versionprobe.Config{Name: "mcode", DefaultBinary: defaultBinary(), MissingError: ErrCLINotFound, TrimBinary: true})
	if err == nil && version != SupportedVersion {
		err = fmt.Errorf("mcode: unsupported version %s; install %s", version, SupportedVersion)
	}
	return version, err
}
