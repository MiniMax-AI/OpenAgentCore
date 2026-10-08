//go:build !linux

package cli

import (
	"context"
	"errors"
)

// nativeBundlePrograms are the distribution's programs besides oac-daemon.
var nativeBundlePrograms []string

// errSandboxPlatform rejects start off Linux: oac-sandbox-io, which serves a
// self-hosted machine, runs only on Linux.
var errSandboxPlatform = errors.New("start: self-hosted Environments run only on Linux")

func runSandboxLauncher(context.Context, *runContext, bool, string, nativeInstallation) error {
	return errSandboxPlatform
}
