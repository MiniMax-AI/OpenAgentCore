package cli

import (
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/daemonize"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// publishConnectedState records the authenticated connection so the Runtime
// readiness endpoint can report it. The Cube template probe and Core's
// BootstrapComplete evidence both read this file through that endpoint, and the
// daemon is its only writer.
func publishConnectedState(profile, deviceID string) {
	path, err := paths.ConnectedStateFile(profile)
	if err != nil {
		return
	}
	if err := daemonize.WriteConnectedState(path, deviceID); err != nil {
		obslog.Bg().Warn("cannot publish connected state", "err", err)
	}
}

// clearConnectedState withdraws the statement when the session ends, so a lost or
// refused connection is never reported as readiness.
func clearConnectedState(profile string) {
	path, err := paths.ConnectedStateFile(profile)
	if err != nil {
		return
	}
	if err := daemonize.RemoveConnectedState(path); err != nil {
		obslog.Bg().Warn("cannot withdraw connected state", "err", err)
	}
}
