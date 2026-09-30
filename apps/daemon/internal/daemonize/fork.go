// Package daemonize gives `oac-daemon connect -b` a no-cgo way to
// detach from the controlling terminal on macOS, Linux and Windows. Strategy is
// re-exec-the-binary rather than POSIX double-fork: the parent opens
// connect.log + connect.pid, then starts a fresh copy of its own
// argv with stdio redirected to the log file and a sentinel env var
// set so the child skips the fork branch. Setsid puts the child into
// its own session so closing the user's shell doesn't kill the daemon.
//
// Re-exec (not raw fork) because Go's runtime is not fork-safe — the
// goroutine scheduler holds locks the child can't release without
// exec. exec.Cmd.Start does fork+exec, which is the safe combination.
package daemonize

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// BackgroundSentinelEnv is set by the parent on the child's
// environment so the child knows it's the post-fork incarnation and
// must NOT itself try to re-fork. runConnect inspects via
// IsBackgroundChild.
const BackgroundSentinelEnv = "OAC_RUNTIME_DAEMON_BACKGROUND_CHILD"

// IsBackgroundChild reports whether this process was spawned by a
// `connect -b` re-exec. runConnect skips the fork branch when true,
// otherwise the child would spawn grandchildren forever.
func IsBackgroundChild() bool {
	return os.Getenv(BackgroundSentinelEnv) == "1"
}

// ReExecOptions controls how Spawn launches the background child.
type ReExecOptions struct {
	// LogPath is the absolute log file path. Child stdin is
	// /dev/null; stdout+stderr are appended to this file (0o600).
	LogPath string

	// PIDPath is the absolute process-record path. The parent writes the
	// child identity here before returning; existing files are replaced
	// atomically.
	PIDPath string

	// ExtraEnv is appended to the child's environment in addition to
	// parent environ + BackgroundSentinelEnv.
	ExtraEnv []string
}

// Spawn re-execs the current binary in the background. argv is the
// new process's full argv including argv[0]. Spawn does NOT scrub
// `-b` — BackgroundSentinelEnv is what tells the child to skip the
// fork.
//
// On error the partially-opened log file is closed and a best-effort
// pidfile cleanup runs so a half-spawn doesn't leave stale state.
func Spawn(argv []string, opts ReExecOptions) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("daemonize.Spawn: empty argv")
	}
	if opts.LogPath == "" || opts.PIDPath == "" {
		return 0, errors.New("daemonize.Spawn: LogPath and PIDPath required")
	}

	logFile, err := openPrivateLog(opts.LogPath)
	if err != nil {
		return 0, fmt.Errorf("daemonize: open log %s: %w", opts.LogPath, err)
	}
	defer logFile.Close() // child gets its own dup via cmd.Stdout/Stderr

	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return 0, fmt.Errorf("daemonize: open /dev/null: %w", err)
	}
	defer devNull.Close()

	// Prefer the absolute path the parent was invoked with so a child
	// started from `./bin/oac-daemon` doesn't re-exec a different
	// binary on PATH.
	exe, err := os.Executable()
	if err != nil {
		// Fall back to argv[0] if /proc/self/exe or
		// _NSGetExecutablePath fail. Worst case: child re-execs
		// whatever's on PATH under the same name — still oac-daemon
		// in practice.
		exe = argv[0]
	}

	env := append([]string(nil), os.Environ()...)
	env = append(env, BackgroundSentinelEnv+"=1")
	env = append(env, opts.ExtraEnv...)

	cmd := exec.Command(exe, argv[1:]...)
	cmd.Env = env
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cleanup, ready, err := configureBackground(cmd)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("daemonize: start child: %w", err)
	}

	pid := cmd.Process.Pid
	if err := ready(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, err
	}
	identity, err := identifyProcess(pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, err
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, stopEventEnv+"=") {
			identity.StopEvent = strings.TrimPrefix(entry, stopEventEnv+"=")
		}
	}

	if err := writeIdentity(opts.PIDPath, identity); err != nil {
		// The parent still owns this exact process handle until publication succeeds.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, fmt.Errorf("daemonize: write process record: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		fmt.Fprintf(os.Stderr, "daemonize: release child handle: %v\n", err)
	}

	return pid, nil
}
