package cli

import "github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/transport"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
	"github.com/gorilla/websocket"
	"sync/atomic"
	"testing"
	"time"
)

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
	for _, suspend := range []bool{false, true} {
		name := "ordinary"
		if suspend {
			name = "suspension-enabled"
		}
		t.Run(name, func(t *testing.T) { testDisconnectedPumpCleanup(t, suspend) })
	}
}

func testDisconnectedPumpCleanup(t *testing.T, suspend bool) {
	t.Setenv("OAC_RUNTIME_WORKSPACE", "")
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
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "cleanup", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})},
		harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, errors.New("unexpected legacy factory")
		})
	var factories atomic.Int32
	registry.RegisterExecutor("cleanup", func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
		factories.Add(1)
		return owner, nil
	})
	finished := make(chan error, 1)
	go func() {
		boot := &transport.BootstrapResponse{HeartbeatSeconds: 60}
		if suspend {
			control := &suspendControl{signal: make(chan os.Signal, 1)}
			finished <- runSuspendLoop(ctx, dial, registry, boot, agentCLIDiscovery{}, control)
			return
		}
		conn, err := dial(ctx)
		if err != nil {
			finished <- err
			return
		}
		defer conn.Close()
		finished <- pumpConn(ctx, conn, registry, boot, agentCLIDiscovery{})
	}()
	var peer *websocket.Conn
	select {
	case peer = <-peers:
	case <-time.After(3 * time.Second):
		t.Fatal("initial connection missing")
	}
	defer peer.Close()
	env, err := proto.NewEnvelope(proto.TypeExecutionPrepare, "prepare", proto.ExecutionPreparePayload{SessionID: "cleanup",
		Configuration: proto.PromptRequestPayload{AgentKind: "cleanup", AgentStateKey: "agents-api-cleanup", StrictResume: true, DisableExecutionEnvironment: true}})
	if err != nil {
		t.Fatal(err)
	}
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
