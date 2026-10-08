package mcode

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/binpath"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/versionprobe"
	configuration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

var ErrCLINotFound = errors.New("mcode CLI not found")

const SupportedVersion = configuration.NativeVersion

func defaultBinary() string { return binpath.MCode() }

func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	version, err := versionprobe.Check(ctx, binary, versionprobe.Config{Name: "mcode", DefaultBinary: defaultBinary(), MissingError: ErrCLINotFound, TrimBinary: true})
	if err == nil && version != SupportedVersion {
		err = fmt.Errorf("mcode: unsupported version %s; install %s", version, SupportedVersion)
	}
	return version, err
}
