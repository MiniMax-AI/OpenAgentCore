package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/auth"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/daemonize"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/transport"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

const (
	bootstrapTimeout = 10 * time.Second

	// Allow the native process grace period and subsequent owner/pipe cleanup.
	stopTimeout = 10 * time.Second
)

// runConnect dials /agent-daemon/bootstrap, opens /agent-daemon/ws,
// wires the dispatch router, and routes Envelope traffic both ways
// until either SIGINT/SIGTERM or a permanent credential rejection.
//
// The daemon credential comes from a Provider bootstrap file
// (--bootstrap-file), self-hosted Environment enrollment
// (--remote/--environment-id/--credential-file) or the saved profile
// written by oac-core-device.
//
// -b re-execs the binary in the background with stdio redirected to
// connect.log and the child PID written to connect.pid. The child
// re-enters runConnect via BackgroundSentinelEnv.
func runConnect(ctx *runContext, args []string) error {
	fs := newFlagSet("connect")
	var (
		profile        = fs.String("profile", paths.DefaultProfile, "profile name for daemon credentials and pid/log files")
		background     = fs.Bool("b", false, "fork into the background; writes connect.pid + connect.log")
		remote         = fs.String("remote", "", "self-hosted Environment remote_url, unchanged")
		environment    = fs.String("environment-id", "", "self-hosted Environment ID")
		bootstrapFile  = fs.String("bootstrap-file", "", "absolute path to Provider-to-Runtime connection JSON")
		credentialFile = fs.String("credential-file", "", "absolute path to protected executor credential JSON")
	)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("connect: parse flags: %w", err)
	}
	installation, err := nativeInstallationPath()
	if err != nil {
		return err
	}
	if _, err = os.Lstat(installation); err == nil {
		return errors.New("connect: this Runtime has a native installation; use oac-daemon start to validate its installed Harnesses")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("connect: cannot inspect native installation; use oac-daemon start")
	}
	if err := paths.ValidateProfile(*profile); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	var bootstrapped *auth.Profile
	if *bootstrapFile != "" {
		if *remote != "" || *environment != "" || *credentialFile != "" || fs.NArg() != 0 {
			return errors.New("connect: bootstrap input cannot be combined with enrollment options")
		}
		bootstrapped, err = bootstrapProfile(*bootstrapFile)
		if err != nil {
			return err
		}
	}
	if *remote != "" || *environment != "" || *credentialFile != "" {
		if fs.NArg() != 0 {
			return errors.New("connect: Environment enrollment cannot use positional arguments")
		}
		connectCtx, stop := daemonize.NotifyContext(context.Background())
		defer stop()
		return runEnvironmentConnect(connectCtx, ctx, *profile, *background, *remote, *environment, *credentialFile)
	}

	// -b mode: parent forks, child re-enters with sentinel env set
	// and skips this branch.
	if *background && !daemonize.IsBackgroundChild() {
		// Validate auth.json exists before forking so the error
		// surfaces in the user's terminal instead of the background
		// child's log.
		if bootstrapped == nil {
			if _, err := auth.Load(*profile); err != nil {
				return fmt.Errorf("connect: %w", err)
			}
		}
		return spawnBackground(context.Background(), ctx, *profile, os.Args)
	}

	// Self-check before loading credentials so a machine with no
	// supported agent CLI fails fast.
	agentCLIs, err := preflightAgentCLIs(context.Background(), ctx, *profile)
	if err != nil {
		return err
	}

	var prof auth.Profile
	if bootstrapped != nil {
		prof = *bootstrapped
	} else {
		prof, err = auth.Load(*profile)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
	}

	return mainLoop(ctx, *profile, prof, agentCLIs)
}

// spawnBackground forks the daemon into the background. Parent
// returns after printing the child PID; child re-enters runConnect
// with BackgroundSentinelEnv set so the same mainLoop runs in either
// mode.
func spawnBackground(ctx context.Context, rc *runContext, profile string, argv []string) error {
	logPath, err := paths.LogFile(profile)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	pidPath, err := paths.PIDFile(profile)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	// Serialize the live-process check and publication across concurrent starts.
	if err := runtimefs.EnsurePrivateDir(filepath.Dir(pidPath)); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(pidPath))
	if err != nil {
		return err
	}
	defer root.Close()
	unlock, err := runtimefs.LockDirectory(root)
	if err != nil {
		return errors.New("connect: startup is busy; wait and retry")
	}
	defer unlock()
	// Refuse to start a second background daemon for the same profile.
	if pid, err := daemonize.ReadPIDFile(pidPath); err == nil {
		return fmt.Errorf("connect: background daemon already running (pid=%d); run `oac-daemon stop` first", pid)
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, daemonize.ErrStaleOrCorrupt) {
		return fmt.Errorf("connect: check pidfile: %w", err)
	}
	// Stale pidfile → remove so Spawn starts clean.
	_ = daemonize.RemovePIDFile(pidPath)

	if err := daemonize.EnsureLogFile(logPath); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	pid, err := daemonize.Spawn(argv, daemonize.ReExecOptions{
		LogPath: logPath,
		PIDPath: pidPath,
	})
	if err != nil {
		return fmt.Errorf("connect: spawn background: %w", err)
	}

	if err := ctx.Err(); err != nil {
		if stopErr := daemonize.StopPIDFile(pidPath, stopTimeout); stopErr != nil {
			return fmt.Errorf("connect: interrupted startup cleanup: %w", stopErr)
		}
		return err
	}
	fmt.Fprintf(rc.stdout, "oac-daemon: backgrounded (pid=%d)\n", pid)
	fmt.Fprintf(rc.stdout, "  logs : %s\n", logPath)
	fmt.Fprintf(rc.stdout, "  pid  : %s\n", pidPath)
	fmt.Fprintf(rc.stdout, "  stop : oac-daemon stop --profile %s\n", profile)
	return nil
}

// mainLoop is the daemon body — runs in foreground and in the re-execed
// background process. SIGINT / SIGTERM cancels the root context, which
// unblocks the read pump and any in-flight Send so the daemon exits
// without orphaning agent subprocesses.
func mainLoop(rc *runContext, profile string, prof auth.Profile, agentCLIs agentCLIDiscovery) error {
	return mainLoopRemote(context.Background(), rc, profile, prof, agentCLIs, "")
}

func mainLoopRemote(parent context.Context, rc *runContext, profile string, prof auth.Profile, agentCLIs agentCLIDiscovery, remote string) error {
	// Route through obs/log so daemon log lines pick up the same
	// trace_id / span_id auto-injection as the server side — when the
	// daemon adopts an envelope's trace, every log call under that ctx
	// gets the same trace_id so `grep <trace_id>` finds the line on
	// both ends.
	obslog.Init(obslog.Config{
		Format: "text",
		Level:  slog.LevelInfo,
		Out:    rc.stderr,
	})

	rootCtx, cancel := daemonize.NotifyContext(parent)
	defer cancel()

	bootCtx, bootCancel := context.WithTimeout(rootCtx, bootstrapTimeout)
	var boot *transport.BootstrapResponse
	var err error
	if remote == "" {
		boot, err = transport.Bootstrap(bootCtx, prof.ServerURL, prof.RuntimeID, prof.RunnerCredential, Version)
	} else {
		boot, err = environmentBootstrap(bootCtx, prof, remote)
	}
	bootCancel()
	if err != nil {
		return fmt.Errorf("connect: bootstrap: %w", err)
	}
	wsURL, err := transport.DeriveWSURL(*boot, prof.ServerURL)
	if err != nil {
		return fmt.Errorf("connect: derive ws url: %w", err)
	}
	obslog.Bg().Info("bootstrap ok", "device_id", boot.DeviceID, "ws_url", wsURL, "heartbeat_interval", boot.HeartbeatInterval())

	local, err := localworkspace.Load()
	if err != nil {
		return err
	}
	registry := agent.NewRegistry()
	registerAgentKinds(registry, agentCLIs, local.Support())

	control, err := newSuspendControl()
	if err != nil {
		return err
	}
	if control != nil {
		defer control.Close()
	}
	dial := func(ctx context.Context) (*transport.Conn, error) {
		conn, err := transport.Dial(ctx, transport.DialOptions{
			WSURL:      wsURL,
			DeviceID:   boot.DeviceID,
			Credential: prof.RunnerCredential,
			// DaemonVersion is the wire-protocol version, not the build
			// tag: proto.VersionCompatible requires an exact match
			// against proto.Version.
			DaemonVersion: proto.Version,
		})
		if remote != "" && err != nil {
			if errors.Is(err, transport.ErrIncompatibleVersion) {
				return nil, fmt.Errorf("Environment connection rejected: %w: %w", transport.ErrPermanent, transport.ErrIncompatibleVersion)
			}
			if errors.Is(err, transport.ErrPermanent) {
				return nil, fmt.Errorf("Environment connection rejected: %w", transport.ErrPermanent)
			}
			return nil, errors.New("Environment connection failed")
		}
		return conn, err
	}

	if control != nil {
		return runSuspendLoop(rootCtx, dial, registry, local, boot, agentCLIs, control)
	}
	return serveConnections(rootCtx, wsURL, dial, 0, func(conn *transport.Conn) error {
		return pumpConn(rootCtx, conn, dispatch.Config{Registry: registry, Environments: localEnvironments(local)}, boot)
	})
}

// serveConnections dials Core with dial and serves each connection until ctx
// ends or Core rejects the credential. With a positive unreachable bound it
// fails once Core has stayed unreachable that long.
func serveConnections(ctx context.Context, wsURL string, dial transport.DialFn, unreachable time.Duration, serve func(*transport.Conn) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		dialCtx, stop := ctx, context.CancelFunc(func() {})
		if unreachable > 0 {
			dialCtx, stop = context.WithTimeout(ctx, unreachable)
		}
		conn, err := transport.Reconnect(dialCtx, dial, transport.DefaultBackoff, func(attempt int, lastDelay time.Duration, lastErr error) {
			switch {
			case attempt == 1:
				obslog.Bg().Info("connecting", "ws_url", wsURL)
			case lastErr != nil:
				// Include lastErr so a stuck Reconnect tells the
				// operator WHY ("ws upgrade rejected with 426")
				// instead of just "retry attempt 3 after 4s".
				obslog.Bg().Warn("dial retry", "attempt", attempt, "delay", lastDelay, "err", lastErr)
			default:
				obslog.Bg().Warn("dial retry", "attempt", attempt, "delay", lastDelay)
			}
		})
		stop()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, transport.ErrPermanent) {
				return fmt.Errorf("connect: permanent error (reissue the daemon credential): %w", err)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("connect: Core unreachable for %s: %w", unreachable, err)
			}
			return fmt.Errorf("connect: dial: %w", err)
		}
		obslog.Bg().Info("ws connected", "device_id", conn.DeviceID())

		// serve returns on conn close (peer hangup, transport error, ctx
		// cancel). Loop back into Reconnect unless ctx is cancelled.
		pumpErr := serve(conn)
		if pumpErr != nil {
			obslog.Bg().Warn("ws session ended", "err", pumpErr)
		} else {
			obslog.Bg().Info("ws session ended cleanly")
		}
		_ = conn.Close()

		// Server-initiated clean close (e.g. shutdown) → exit;
		// otherwise loop back and reconnect.
		if ctx.Err() != nil {
			return nil
		}
		// Permanent error (e.g. runtime deleted) → exit instead of
		// reconnecting.
		if pumpErr != nil && errors.Is(pumpErr, transport.ErrPermanent) {
			return fmt.Errorf("connect: runtime deleted (reissue the daemon credential): %w", pumpErr)
		}
		// Small breather before redialing so a flapping server doesn't
		// get a tight loop of upgrade requests.
		_ = transport.Sleep(ctx, 1*time.Second)
	}
}

// localEnvironments resolves the dedicated local workspace as the Environment
// owner of the one Session it serves.
func localEnvironments(local *localworkspace.Binding) func(proto.AssignmentRef, proto.AssignmentBindPayload) dispatch.Environment {
	if local == nil {
		return nil
	}
	return local.Resolve
}

// pumpConn runs the per-connection workload: a dispatch.Router of cfg's
// Harness kinds and Environment owners fed by conn.Recv(), heartbeats every
// boot.HeartbeatInterval(), and a confirmed router.Shutdown before returning
// ownership to the reconnect loop. Failed cleanup keeps this exact Router
// alive, including after a shutdown signal.
func pumpConn(parentCtx context.Context, conn *transport.Conn, cfg dispatch.Config, boot *transport.BootstrapResponse) error {
	cfg.Sender, cfg.Log = conn, obslog.Bg()
	router, err := dispatch.New(cfg)
	if err != nil {
		return fmt.Errorf("router init: %w", err)
	}
	defer func() {
		_ = conn.Close()
		shutdownRouterUntilConfirmed(router.Shutdown, time.Second)
	}()

	conn.StartHeartbeats(parentCtx, boot.HeartbeatInterval(), func() proto.HeartbeatPayload {
		return proto.HeartbeatPayload{
			SupportedAgentKinds: cfg.Registry.SupportedAgentKinds(),
			HomeRemoval:         proto.CapabilityFromBool(cfg.RemoveHome != nil),
		}
	}, obslog.Bg().With("component", "heartbeat"))

	obslog.Bg().Info("pumpConn: entering recv loop")
	for {
		select {
		case <-parentCtx.Done():
			obslog.Bg().Warn("pumpConn: parentCtx cancelled", "err", parentCtx.Err())
			return parentCtx.Err()
		case <-conn.Done():
			obslog.Bg().Warn("pumpConn: conn.Done fired", "err", conn.Err())
			return conn.Err()
		case env, ok := <-conn.Recv():
			if !ok {
				obslog.Bg().Warn("pumpConn: recvCh closed", "err", conn.Err())
				return conn.Err()
			}
			obslog.Bg().Info("pumpConn: received envelope, calling router.Handle", "type", env.Type, "id", env.ID)
			if err := router.Handle(parentCtx, env); err != nil {
				obslog.Bg().Error("router.Handle failed", "type", env.Type, "id", env.ID, "err", err)
			} else {
				obslog.Bg().Info("pumpConn: router.Handle ok", "type", env.Type, "id", env.ID)
			}
		}
	}
}
