package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/daemonize"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
)

// stopTimeout allows the native process grace period and subsequent
// owner/pipe cleanup.
const stopTimeout = 10 * time.Second

// runStop requests cleanup from the recorded daemon identity. A timeout keeps
// the ownership record; only confirmed exit permits removing it.
func runStop(ctx *runContext, args []string) error {
	fs := newFlagSet("stop")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("stop: parse flags: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("stop: unexpected arguments %q", fs.Args())
	}

	pidPath, err := paths.PIDFile()
	if err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	pid, err := daemonize.ReadPIDFile(pidPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintln(ctx.stdout, "oac-daemon: no background daemon running (no pidfile)")
		return nil
	case errors.Is(err, daemonize.ErrStaleOrCorrupt):
		fmt.Fprintf(ctx.stdout, "oac-daemon: stale pidfile detected (%v); removing\n", err)
		if rmErr := daemonize.RemovePIDFile(pidPath); rmErr != nil {
			return fmt.Errorf("stop: %w", rmErr)
		}
		return nil
	case err != nil:
		return fmt.Errorf("stop: read pidfile: %w", err)
	}

	fmt.Fprintf(ctx.stdout, "oac-daemon: requesting stop for pid=%d\n", pid)
	if err := daemonize.StopPIDFile(pidPath, stopTimeout); err != nil {
		return fmt.Errorf("stop: signal: %w", err)
	}
	fmt.Fprintln(ctx.stdout, "oac-daemon: stopped")
	return nil
}
