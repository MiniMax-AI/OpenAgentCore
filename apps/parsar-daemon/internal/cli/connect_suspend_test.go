package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/transport"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/gorilla/websocket"
)

func TestPlannedReconnectRequiresAuthenticatedMatchingResume(t *testing.T) {
	for _, scenario := range []string{"resume", "rollback", "reconnect", "revoked", "wrong_operation"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("OAC_RUNTIME_WORKSPACE", "")
			var connections atomic.Int32
			peers := make(chan *websocket.Conn, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer credential" {
					http.Error(w, "unauthorized", 401)
					return
				}
				attempt := connections.Add(1)
				if scenario == "revoked" && attempt > 1 {
					http.Error(w, "revoked", 401)
					return
				}
				peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err == nil {
					peers <- peer
				}
			}))
			defer server.Close()
			dial := func(ctx context.Context) (*transport.Conn, error) {
				return transport.Dial(ctx, transport.DialOptions{WSURL: "ws" + strings.TrimPrefix(server.URL, "http"), DeviceID: "device", Credential: "credential", DaemonVersion: proto.Version})
			}
			registry := agent.NewRegistry()
			control := &suspendControl{path: filepath.Join(t.TempDir(), "suspend.json"), identity: suspendIdentity{EnvironmentID: "env"}, signal: make(chan os.Signal, 1)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- runSuspendLoop(ctx, dial, registry, &transport.BootstrapResponse{HeartbeatSeconds: 1}, agentCLIDiscovery{}, control)
			}()
			var first *websocket.Conn
			select {
			case first = <-peers:
			case <-time.After(3 * time.Second):
				t.Fatal("initial connection missing")
			}
			defer first.Close()
			request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "planned"}
			sendLifecycleFrame(t, first, proto.TypeEnvironmentQuiesce, request)
			result := readLifecycleResult(t, first, proto.TypeEnvironmentQuiesced)
			if !result.Accepted {
				t.Fatalf("quiesce rejected: %+v", result)
			}
			// The daemon deliberately closes its old socket. Neither that error nor a
			// heartbeat interval may exit the planned park while awaiting host wake.
			_ = first.SetReadDeadline(time.Now().Add(time.Second))
			for {
				var env proto.Envelope
				if first.ReadJSON(&env) != nil {
					break
				}
			}
			select {
			case err := <-done:
				t.Fatalf("park exited without wake: %v", err)
			case <-time.After(25 * time.Millisecond):
			}
			control.signal <- syscall.SIGUSR1
			if scenario == "revoked" {
				select {
				case err := <-done:
					if !errors.Is(err, transport.ErrPermanent) {
						t.Fatalf("revoke error=%v", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("revoked reconnect did not terminate")
				}
			} else {
				var second *websocket.Conn
				select {
				case second = <-peers:
				case <-time.After(3 * time.Second):
					t.Fatal("resume connection missing")
				}
				defer second.Close()
				if scenario == "rollback" {
					request.Rollback = true
				}
				if scenario == "wrong_operation" {
					request.SuspendID = "stale"
				}
				sendLifecycleFrame(t, second, proto.TypeEnvironmentResume, request)
				if scenario == "resume" || scenario == "rollback" || scenario == "reconnect" {
					result = readLifecycleResult(t, second, proto.TypeEnvironmentResumed)
					if !result.Accepted {
						t.Fatalf("resume failed: %+v", result)
					}
					// A lost response may be retried on this same authenticated socket.
					sendLifecycleFrame(t, second, proto.TypeEnvironmentResume, request)
					if repeated := readLifecycleResult(t, second, proto.TypeEnvironmentResumed); !repeated.Accepted {
						t.Fatal("resume receipt was not idempotent")
					}
					if scenario == "reconnect" {
						// Core can lose the receipt before committing waking -> running.
						// The next ordinary connection has a fresh Router but must confirm
						// this consumed token without restoring or retaining old work.
						_ = second.Close()
						var third *websocket.Conn
						select {
						case third = <-peers:
						case <-time.After(3 * time.Second):
							t.Fatal("ordinary reconnect missing")
						}
						defer third.Close()
						sendLifecycleFrame(t, third, proto.TypeEnvironmentResume, request)
						if repeated := readLifecycleResult(t, third, proto.TypeEnvironmentResumed); !repeated.Accepted {
							t.Fatal("receipt lost across ordinary reconnect")
						}
						request.SuspendID = "unrelated"
						sendLifecycleFrame(t, third, proto.TypeEnvironmentResume, request)
						if unrelated := readLifecycleResult(t, third, proto.TypeEnvironmentResumed); unrelated.Accepted {
							t.Fatal("unrelated resume accepted by fresh Router")
						}
					}
					cancel()
				}
				select {
				case err := <-done:
					if scenario == "wrong_operation" && err == nil {
						t.Fatal("mismatched resume accepted")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("lifecycle failed to terminate")
				}
			}
		})
	}
}

func sendLifecycleFrame(t *testing.T, peer *websocket.Conn, kind string, payload proto.EnvironmentSuspendPayload) {
	t.Helper()
	env, err := proto.NewEnvelope(kind, "operation", payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.WriteJSON(env); err != nil {
		t.Fatal(err)
	}
}
func readLifecycleResult(t *testing.T, peer *websocket.Conn, kind string) proto.EnvironmentSuspendResultPayload {
	t.Helper()
	_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var env proto.Envelope
		if err := peer.ReadJSON(&env); err != nil {
			t.Fatal(err)
		}
		if env.Type != kind {
			continue
		}
		var result proto.EnvironmentSuspendResultPayload
		if err := env.DecodePayload(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
}

func TestRunningSourceOnlyAcceptsExplicitRollbackOrItsReceipt(t *testing.T) {
	t.Setenv("OAC_RUNTIME_WORKSPACE", "")
	peers := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			peers <- peer
		}
	}))
	defer server.Close()
	dial := func(ctx context.Context) (*transport.Conn, error) {
		return transport.Dial(ctx, transport.DialOptions{WSURL: "ws" + strings.TrimPrefix(server.URL, "http"), DeviceID: "device", Credential: "credential", DaemonVersion: proto.Version})
	}
	control := &suspendControl{path: filepath.Join(t.TempDir(), "control.json"), identity: suspendIdentity{EnvironmentID: "env"}, signal: make(chan os.Signal, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runSuspendLoop(ctx, dial, agent.NewRegistry(), &transport.BootstrapResponse{HeartbeatSeconds: 60}, agentCLIDiscovery{}, control)
	}()
	var peer *websocket.Conn
	select {
	case peer = <-peers:
	case <-time.After(time.Second):
		t.Fatal("no connection")
	}
	defer peer.Close()
	for _, test := range []struct {
		request  proto.EnvironmentSuspendPayload
		accepted bool
	}{
		{proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "cold_restore"}, false},
		{proto.EnvironmentSuspendPayload{EnvironmentID: "wrong", SuspendID: "rollback", Rollback: true}, false},
		{proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "rollback", Rollback: true}, true},
		{proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "rollback"}, true},
		{proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "cold_restore"}, false},
	} {
		sendLifecycleFrame(t, peer, proto.TypeEnvironmentResume, test.request)
		got := readLifecycleResult(t, peer, proto.TypeEnvironmentResumed)
		if got.Accepted != test.accepted {
			t.Fatalf("request=%+v result=%+v", test.request, got)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not settle")
	}
}

func TestSuspensionReconnectBeforeConfirmation(t *testing.T) {
	for _, scenario := range []string{"disconnect", "timeout", "revoked", "cancelled", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("OAC_RUNTIME_WORKSPACE", "")
			var attempts atomic.Int32
			peers := make(chan *websocket.Conn, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := attempts.Add(1)
				if scenario == "revoked" && attempt > 1 {
					http.Error(w, "revoked", http.StatusUnauthorized)
					return
				}
				peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err == nil {
					peers <- peer
				}
			}))
			defer server.Close()
			dial := func(ctx context.Context) (*transport.Conn, error) {
				return transport.Dial(ctx, transport.DialOptions{WSURL: "ws" + strings.TrimPrefix(server.URL, "http"), DeviceID: "device", Credential: "credential", DaemonVersion: proto.Version})
			}
			state, err := newSuspendedRouter(nil, agent.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			defer state.shutdown()
			request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "pending-confirmation"}
			if err := state.router.Quiesce(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			control := &suspendControl{path: filepath.Join(t.TempDir(), "control.json"), identity: suspendIdentity{EnvironmentID: "env"}, signal: make(chan os.Signal, 1)}
			if err := control.Arm(request); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				conn, err := state.reconnectSuspension(ctx, dial, request, control, 100*time.Millisecond)
				if conn != nil {
					_ = conn.Close()
				}
				done <- err
			}()
			var first *websocket.Conn
			select {
			case first = <-peers:
			case <-time.After(3 * time.Second):
				t.Fatal("initial recovery connection missing")
			}
			defer first.Close()
			switch scenario {
			case "cancelled":
				cancel()
			case "deleted":
				if err := first.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "runtime deleted"), time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			case "timeout":
				// Leave the connection open without confirmation. The short injected
				// attempt deadline must cause a new authenticated connection.
			default:
				_ = first.Close()
			}
			if scenario == "disconnect" || scenario == "timeout" {
				var second *websocket.Conn
				select {
				case second = <-peers:
				case err := <-done:
					t.Fatalf("transient failure discarded suspension: %v", err)
				case <-time.After(3 * time.Second):
					t.Fatal("recovery retry missing")
				}
				defer second.Close()
				sendLifecycleFrame(t, second, proto.TypeEnvironmentResume, request)
				if result := readLifecycleResult(t, second, proto.TypeEnvironmentResumed); !result.Accepted {
					t.Fatal("matching resume rejected after retry")
				}
			}
			select {
			case err := <-done:
				switch scenario {
				case "revoked", "deleted":
					if !errors.Is(err, transport.ErrPermanent) {
						t.Fatalf("permanent rejection=%v", err)
					}
				case "cancelled":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancellation=%v", err)
					}
				default:
					if err != nil || control.lastResumed == nil || !control.lastResumed.SameSuspension(request) {
						t.Fatalf("recovery did not preserve suspension: %v", err)
					}
				}
			case <-time.After(3 * time.Second):
				t.Fatal("recovery failed to settle")
			}
		})
	}
}
