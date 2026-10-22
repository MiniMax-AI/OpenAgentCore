package mcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/binpath"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/versionprobe"
)

var ErrCLINotFound = errors.New("mcode CLI not found")

const SupportedVersion = "0.4.12"

func defaultBinary() string { return binpath.MCode() }

// defaultNode is the Node.js that runs a JavaScript CLI entrypoint.
func defaultNode() string {
	if node := strings.TrimSpace(os.Getenv("OAC_RUNTIME_MCODE_NODE")); node != "" {
		return node
	}
	return "node"
}

// nativeCommand returns the program and leading arguments that start the CLI.
// A JavaScript entrypoint, such as the packaged native/cli.js, runs under the
// given Node.js: Windows cannot execute it directly, and on Unix this selects
// the same pinned Node.js that its shebang would find first on PATH.
func nativeCommand(binary, node string) []string {
	if strings.EqualFold(filepath.Ext(binary), ".js") {
		return []string{node, binary}
	}
	return []string{binary}
}

func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	if binary = strings.TrimSpace(binary); binary == "" {
		binary = strings.TrimSpace(defaultBinary())
	}
	command := nativeCommand(binary, defaultNode())
	if len(command) > 1 {
		if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("%w: %s", ErrCLINotFound, binary)
		}
	}
	version, err := versionprobe.Check(ctx, command[0], versionprobe.Config{Name: "mcode", Args: command[1:], DefaultBinary: command[0], MissingError: ErrCLINotFound, TrimBinary: true})
	if err == nil && version != SupportedVersion {
		err = fmt.Errorf("mcode: unsupported version %s; install %s", version, SupportedVersion)
	}
	return version, err
}
