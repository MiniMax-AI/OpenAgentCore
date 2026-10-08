//go:build !linux

package cli

import (
	"context"
)

// nativeBundlePrograms are the distribution's programs besides oac-daemon.
var nativeBundlePrograms []string

func runSandboxLauncher(context.Context, *runContext, bool, string, nativeInstallation) error {
	return requireNativePlatform()
}
