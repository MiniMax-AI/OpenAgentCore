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
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
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
	view := &agent.View{Proxy: agent.ViewProxyEnv, Executor: func(context.Context, agent.PrepareRequest, agent.ViewSession) (agent.Executor, error) {
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

func TestReconnectWaitsForConfirmedRouterCleanup(t *testing.T) {
	for _, failure := range []error{errors.New("native close failed"), context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			var calls atomic.Int32
			retry := make(chan struct{})
			confirm := make(chan struct{})
			returned := make(chan struct{})
			go func() {
				shutdownRouterUntilConfirmed(func(ctx context.Context) error {
					if ctx.Err() != nil {
						t.Error("cleanup inherited a cancelled connection context")
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Error("cleanup wait has no deadline")
					}
					if calls.Add(1) == 1 {
						return failure
					}
					close(retry)
					<-confirm
					return nil
				}, time.Millisecond)
				close(returned)
			}()
			select {
			case <-retry:
			case <-time.After(time.Second):
				t.Fatal("same cleanup was not retried")
			}
			select {
			case <-returned:
				t.Fatal("reconnect released unconfirmed ownership")
			default:
			}
			close(confirm)
			select {
			case <-returned:
			case <-time.After(time.Second):
				t.Fatal("confirmed cleanup did not release reconnect")
			}
			if calls.Load() != 2 {
				t.Fatalf("cleanup attempts=%d", calls.Load())
			}
		})
	}
}

type cleanupExecutor struct {
	closes  atomic.Int32
	retry   chan struct{}
	confirm chan struct{}
}

func (e *cleanupExecutor) StartTurn(context.Context, string, proto.MessageInput, chan<- proto.Envelope) (agent.Turn, error) {
	return nil, errors.New("unexpected Turn")
}
func (e *cleanupExecutor) Close(context.Context) error {
	if e.closes.Add(1) == 1 {
		return errors.New("native cleanup temporarily unavailable")
	}
	close(e.retry)
	<-e.confirm
	return nil
}

func TestDisconnectedPumpRetainsExactExecutorUntilCleanup(t *testing.T) {
	peers := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			peers <- peer
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dial := func(ctx context.Context) (*transport.Conn, error) {
		return transport.Dial(ctx, transport.DialOptions{
			WSURL: "ws" + strings.TrimPrefix(server.URL, "http"), DeviceID: "device",
			Credential: "credential", DaemonVersion: proto.Version,
		})
	}
	owner := &cleanupExecutor{retry: make(chan struct{}), confirm: make(chan struct{})}
	registry := agent.NewRegistry()
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "cleanup", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})}, prototest.ModelConfiguration())
	var factories atomic.Int32
	registry.RegisterExecutor("cleanup", func(context.Context, agent.PrepareRequest) (agent.Executor, error) {
		factories.Add(1)
		return owner, nil
	})
	finished := make(chan error, 1)
	go func() {
		boot := &transport.BootstrapResponse{HeartbeatSeconds: 60}
		conn, err := dial(ctx)
		if err != nil {
			finished <- err
			return
		}
		defer conn.Close()
		finished <- pumpConn(ctx, conn, dispatch.Config{Registry: registry}, boot)
	}()
	var peer *websocket.Conn
	select {
	case peer = <-peers:
	case <-time.After(3 * time.Second):
		t.Fatal("initial connection missing")
	}
	defer peer.Close()
	ref := proto.AssignmentRef{SessionID: "cleanup", AssignmentID: "assignment", Epoch: 1}
	if got := sendAssignment(t, peer, proto.TypeAssignmentBind, ref, proto.AssignmentBindPayload{}); got.State != proto.AssignmentBound {
		t.Fatalf("bind = %+v", got)
	}
	env, err := proto.NewEnvelope(proto.TypeExecutionPrepare, "prepare", proto.ExecutionPreparePayload{SessionID: "cleanup",
		Configuration: prototest.WithModel(proto.PromptRequestPayload{AgentKind: "cleanup", DisableExecutionEnvironment: true})})
	if err != nil {
		t.Fatal(err)
	}
	env.Assignment = ref
	if err := peer.WriteJSON(env); err != nil {
		t.Fatal(err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var result proto.Envelope
		if err := peer.ReadJSON(&result); err != nil {
			t.Fatal(err)
		}
		if result.Type != proto.TypePreparationStatus {
			continue
		}
		var status proto.PreparationStatusPayload
		if err := result.DecodePayload(&status); err != nil {
			t.Fatal(err)
		}
		if status.State == "ready" {
			break
		}
		if status.State == "failed" {
			t.Fatalf("preparation failed: %+v", status)
		}
	}
	_ = peer.Close()
	select {
	case <-owner.retry:
	case <-time.After(3 * time.Second):
		t.Fatal("original executor cleanup was not retried")
	}
	select {
	case err := <-finished:
		t.Fatalf("pump discarded native ownership: %v", err)
	default:
	}
	cancel()
	close(owner.confirm)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("pump did not return after confirmed cleanup")
	}
	if factories.Load() != 1 || owner.closes.Load() != 2 {
		t.Fatalf("factory=%d close=%d", factories.Load(), owner.closes.Load())
	}
}

func sendAssignment(t *testing.T, peer *websocket.Conn, kind string, ref proto.AssignmentRef, payload any) proto.AssignmentStatusPayload {
	t.Helper()
	env, err := proto.NewEnvelope(kind, kind, payload)
	if err != nil {
		t.Fatal(err)
	}
	env.Assignment = ref
	if err := peer.WriteJSON(env); err != nil {
		t.Fatal(err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var reply proto.Envelope
		if err := peer.ReadJSON(&reply); err != nil {
			t.Fatal(err)
		}
		var status proto.AssignmentStatusPayload
		if reply.Type == proto.TypeAssignmentStatus && reply.ID == kind && reply.Assignment == ref && reply.DecodePayload(&status) == nil {
			return status
		}
	}
}
