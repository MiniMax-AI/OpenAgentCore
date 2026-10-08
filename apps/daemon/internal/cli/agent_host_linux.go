//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agenthost"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/daemonize"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/transport"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// The agent-host image's layout (deploy/distribution/AgentHost.Dockerfile)
// and the container's own state.
const (
	agentHostManifest = "/opt/oac/harnesses.json"
	agentHostShim     = "/opt/oac/bin/oac-process-shim"
	agentHostCADir    = "/usr/share/ca-certificates/mozilla"
	// agentHostState keeps each Session's home across restarts.
	agentHostState = "/var/lib/oac/agent-host"
	// agentHostCgroup is where the agent host mounts the container's own
	// cgroup v2 hierarchy; its views directory is delegated to the views.
	agentHostCgroup = "/run/oac/cgroup"
	// agentHostUnreachable is how long Core may stay unreachable before the
	// agent host exits, so that its supervisor restarts it.
	agentHostUnreachable = 2 * time.Minute
)

// agentHostUIDs is the range the agent host runs its Executors as.
var agentHostUIDs = agenthost.UIDRange{First: 70000, Count: 4096}

func runAgentHost(rc *runContext, args []string) error {
	return serveAgentHost(context.Background(), rc, args, harnessDeclarations)
}

// serveAgentHost runs the agent host in its container: the Harnesses that
// the image's manifest installs and that declare a view serve Sessions bound
// to the Runtime of the identity, whose Environments are the agent host's.
func serveAgentHost(parent context.Context, rc *runContext, args []string, declarations []agent.Declaration) error {
	flags := newFlagSet("agent-host")
	identityFile := flags.String("identity-file", "", "path to the agent host's identity JSON")
	coreURL := flags.String("core-url", "", "Core origin, https or a loopback http origin")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("agent-host: parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("agent-host: unexpected arguments %q", flags.Args())
	}
	var identity struct {
		RuntimeID  string `json:"runtime_id"`
		Credential string `json:"credential"`
	}
	raw, err := os.ReadFile(*identityFile)
	if err != nil {
		return fmt.Errorf("agent-host: identity: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil {
		return fmt.Errorf("agent-host: identity: %w", err)
	}
	runtimeID, err := uuid.Parse(identity.RuntimeID)
	if err != nil || runtimeID.String() != identity.RuntimeID || identity.Credential == "" {
		return errors.New("agent-host: identity needs a canonical runtime_id and a credential")
	}
	// Core derives the same URLs from OAC_PUBLIC_URL; its Link exists only
	// on https or a loopback origin, which agenthost.Open checks.
	origin, err := url.Parse(*coreURL)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" || origin.User != nil ||
		origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.String() != *coreURL {
		return errors.New("agent-host: --core-url is not an http or https origin")
	}
	wsOrigin := "ws" + strings.TrimPrefix(*coreURL, "http")

	obslog.Init(obslog.Config{Format: "text", Level: slog.LevelInfo, Out: rc.stderr})
	ctx, cancel := daemonize.NotifyContext(parent)
	defer cancel()

	env, err := agent.ManifestEnvironment(agentHostManifest, harnessInstallations...)
	if err != nil {
		return fmt.Errorf("agent-host: %w", err)
	}
	for name, value := range env {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("agent-host: %w", err)
		}
	}
	harnesses := agent.NewRegistry()
	for _, declaration := range declarations {
		runtime := declaration.Discover(ctx, agent.DiscoveryOptions{Stdout: rc.stdout, Stderr: rc.stderr}, declaration.Info)
		if runtime == nil || runtime.View == nil {
			continue
		}
		harnesses.RegisterKind(runtime.Info, declaration.Configuration)
		harnesses.RegisterView(runtime.Info.Kind, *runtime.View)
	}

	// With a private cgroup namespace, cgroup v2 mounted again is the
	// container's own cgroup, writable unlike Docker's mount of it.
	views := filepath.Join(agentHostCgroup, "views")
	if err := os.MkdirAll(agentHostCgroup, 0o755); err != nil {
		return fmt.Errorf("agent-host: %w", err)
	}
	if err := unix.Mount("cgroup2", agentHostCgroup, "cgroup2", 0, ""); err != nil {
		return fmt.Errorf("%w: agent-host: mount cgroup v2: %w", agenthost.ErrUnsupported, err)
	}
	if err := os.Mkdir(views, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: agent-host: view cgroups: %w", agenthost.ErrUnsupported, err)
	}
	host, err := agenthost.Open(agenthost.Config{StateDir: agentHostState, UIDs: agentHostUIDs, ViewCgroups: views,
		RelayURL: wsOrigin + "/api/v1/sandbox-link", RuntimeID: sandboxwire.ID(runtimeID), Credential: []byte(identity.Credential),
		Harnesses: harnesses, Shim: agentHostShim, CADir: agentHostCADir, Log: obslog.Bg()})
	if err != nil {
		return fmt.Errorf("agent-host: %w", err)
	}
	defer host.Close()

	// Bootstrap on each dial, so a Core that is still starting is retried
	// with the connection's backoff. The agent host dials Core's origin, not
	// the bootstrap's public ws_url.
	wsURL := wsOrigin + "/api/v1/agent-daemon/ws"
	var boot *transport.BootstrapResponse
	dial := func(ctx context.Context) (*transport.Conn, error) {
		b, err := transport.Bootstrap(ctx, *coreURL+"/api/v1", identity.RuntimeID, identity.Credential, Version)
		if err != nil {
			return nil, err
		}
		boot = b
		return transport.Dial(ctx, transport.DialOptions{WSURL: wsURL, DeviceID: identity.RuntimeID, Credential: identity.Credential, DaemonVersion: proto.Version})
	}
	cfg := dispatch.Config{Registry: host.Registry(), Environments: host.Environments, RemoveHome: host.RemoveHome}
	return serveConnections(ctx, wsURL, dial, agentHostUnreachable, func(conn *transport.Conn) error {
		return pumpConn(ctx, conn, cfg, boot)
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

// A wait deadline does not revoke native ownership. Keep retrying the same
// Router until it confirms cleanup; reconnect and process exit both wait here.
// Router.Shutdown serializes attempts and retains resources after a failure.
func shutdownRouterUntilConfirmed(shutdown func(context.Context) error, retryDelay time.Duration) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := shutdown(ctx)
		cancel()
		if err == nil {
			return
		}
		obslog.Bg().Warn("router cleanup unconfirmed; reconnect remains blocked", "err", err)
		time.Sleep(retryDelay)
	}
}
