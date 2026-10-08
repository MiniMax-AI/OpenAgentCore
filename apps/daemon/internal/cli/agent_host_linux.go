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
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/claudesdk"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/codex"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/mcode"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agenthost"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/daemonize"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
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

	env, err := agent.ManifestEnvironment(agentHostManifest, claudesdk.Installation(), codex.Installation(), mcode.Installation())
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
		runtime := declaration.Discover(ctx, agent.DiscoveryOptions{Profile: paths.DefaultProfile, Stdout: rc.stdout, Stderr: rc.stderr}, declaration.Info)
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
