package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/daemonize"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// runStop requests cleanup from the recorded daemon identity. A timeout keeps
// the ownership record; only confirmed exit permits removing it.
func runStop(ctx *runContext, args []string) error {
	fs := newFlagSet("stop")
	profile := fs.String("profile", paths.DefaultProfile, "profile name to stop")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("stop: parse flags: %w", err)
	}
	if err := paths.ValidateProfile(*profile); err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	pidPath, err := paths.PIDFile(*profile)
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
