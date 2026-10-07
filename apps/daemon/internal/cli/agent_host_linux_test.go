//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/transport"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

// TestAgentHostReportsItsDeclarations runs the agent host as its container
// does, against a Core peer, with an image that installs no Harness: once
// without declarations and once with two, of which one has a view. The first
// heartbeat declares exactly the kinds with a view, and home removal.
func TestAgentHostReportsItsDeclarations(t *testing.T) {
	if os.Getenv("OAC_TEST_AGENTHOST") != "1" {
		t.Skip("set OAC_TEST_AGENTHOST=1 and run the test as root in a throwaway container with the agent-host container's flags")
	}
	issuer := httptest.NewTLSServer(nil)
	issuer.Close()
	for path, content := range map[string][]byte{
		agentHostManifest:                       []byte(`{"node": "/usr/local/bin/node", "harnesses": {}}`),
		filepath.Join(agentHostCADir, "ca.crt"): pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuer.Certificate().Raw}),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The agent host presents the credential as written; it is not decoded.
	runtimeID, credential := uuid.NewString(), "c2VjcmV0K/8="
	identity := filepath.Join(t.TempDir(), "identity.json")
	if err := os.WriteFile(identity, []byte(`{"runtime_id": "`+runtimeID+`", "credential": "`+credential+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	heartbeats := make(chan proto.HeartbeatPayload, 1)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential {
			http.Error(w, "credential", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/agent-daemon/bootstrap":
			_ = json.NewEncoder(w).Encode(transport.BootstrapResponse{DeviceID: runtimeID, HeartbeatSeconds: 60})
		case "/api/v1/agent-daemon/ws":
			if r.URL.Query().Get("device_id") != runtimeID {
				http.Error(w, "device", http.StatusUnauthorized)
				return
			}
			peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer peer.Close()
			for {
				var env proto.Envelope
				if err := peer.ReadJSON(&env); err != nil {
					return
				}
				var heartbeat proto.HeartbeatPayload
				if env.Type == proto.TypeHeartbeat && env.DecodePayload(&heartbeat) == nil {
					select {
					case heartbeats <- heartbeat:
					default:
					}
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	declare := func(kind string, view *agent.View) agent.Declaration {
		info := proto.SupportedAgentKind{Kind: kind, Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
			LocalEnvironment: proto.CapabilitySupported, EnvironmentNone: proto.CapabilitySupported})}
		return agent.Declaration{Info: info, Configuration: prototest.ModelConfiguration(),
			Discover: func(context.Context, agent.DiscoveryOptions, proto.SupportedAgentKind) *agent.Runtime {
				return &agent.Runtime{Info: info, View: view}
			}}
	}
	view := &agent.View{Proxy: agent.ViewProxyEnv, Executor: func(context.Context, proto.PromptRequestPayload, agent.ViewSession) (agent.Executor, error) {
		return nil, errors.New("no Executor")
	}}
	// The image may install no Harness: the agent host still connects and
	// declares no kind.
	for _, c := range []struct {
		declarations []agent.Declaration
		kinds        []string
	}{{nil, nil}, {[]agent.Declaration{declare("viewed", view), declare("unviewed", nil)}, []string{"viewed"}}} {
		ctx, cancel := context.WithCancel(t.Context())
		served := make(chan error, 1)
		go func() {
			rc := &runContext{stdin: strings.NewReader(""), stdout: io.Discard, stderr: os.Stderr}
			served <- serveAgentHost(ctx, rc, []string{"--identity-file", identity, "--core-url", core.URL}, c.declarations)
		}()
		select {
		case heartbeat := <-heartbeats:
			var kinds []string
			for _, kind := range heartbeat.SupportedAgentKinds {
				kinds = append(kinds, kind.Kind)
			}
			if !slices.Equal(kinds, c.kinds) || heartbeat.HomeRemoval != proto.CapabilitySupported {
				t.Errorf("heartbeat declares %q with home removal %q, want %q with home removal", kinds, heartbeat.HomeRemoval, c.kinds)
			}
		case err := <-served:
			t.Fatalf("agent host stopped before its first heartbeat: %v", err)
		case <-time.After(30 * time.Second):
			t.Fatal("no heartbeat")
		}
		cancel()
		if err := <-served; err != nil {
			t.Fatalf("agent host stopped with %v, want nil after its signal", err)
		}
		if err := unix.Unmount(agentHostCgroup, 0); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAgentHostExitsWhileCoreStaysUnreachable checks the bound after which
// the agent host exits for its supervisor to restart it.
func TestAgentHostExitsWhileCoreStaysUnreachable(t *testing.T) {
	dial := func(context.Context) (*transport.Conn, error) { return nil, errors.New("connection refused") }
	if err := serveConnections(t.Context(), "ws://127.0.0.1:1", dial, 10*time.Millisecond, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("serveConnections = %v, want the unreachable bound", err)
	}
}
