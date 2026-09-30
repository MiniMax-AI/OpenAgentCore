// Package wireconformance runs the Runtime's production transport and
// dispatcher, with a controlled Harness adapter, against a scripted Core that
// replays the Core frames of the shared wire scenarios.
package wireconformance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/transport"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

const wait = 3 * time.Second

// corePeer is the scripted Core. It accepts the shared handshake and rejects
// any other protocol version as Core does.
type corePeer struct {
	endpoint string
	conns    chan *websocket.Conn
	frames   chan proto.Envelope
	ws       *websocket.Conn
	bindings prototest.Bindings
}

func newCorePeer(t *testing.T) *corePeer {
	t.Helper()
	p := &corePeer{conns: make(chan *websocket.Conn, 4), bindings: prototest.Bindings{}}
	var upgrader websocket.Upgrader
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("device_id") != prototest.DeviceID || r.Header.Get("Authorization") != "Bearer "+prototest.Credential || strings.Contains(r.URL.RawQuery, prototest.Credential) {
			t.Errorf("Runtime handshake: device %q, bearer credential %t, credential in URL %t", query.Get("device_id"), r.Header.Get("Authorization") == "Bearer "+prototest.Credential, strings.Contains(r.URL.RawQuery, prototest.Credential))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if query.Get("version") != proto.Version {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(prototest.IncompatibleVersionStatus)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": prototest.IncompatibleVersionCode})
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		p.conns <- ws
	}))
	t.Cleanup(server.Close)
	p.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	return p
}

// accept takes the Runtime's next connection.
func (p *corePeer) accept(t *testing.T) {
	t.Helper()
	select {
	case ws := <-p.conns:
		t.Cleanup(func() { ws.Close() })
		frames := make(chan proto.Envelope, 16)
		go func() {
			defer close(frames)
			for {
				var env proto.Envelope
				if ws.ReadJSON(&env) != nil {
					return
				}
				frames <- env
			}
		}()
		p.ws, p.frames = ws, frames
	case <-time.After(wait):
		t.Fatal("Runtime did not connect")
	}
}

func (p *corePeer) receive(t *testing.T) proto.Envelope {
	t.Helper()
	select {
	case env, ok := <-p.frames:
		if !ok {
			t.Fatal("Runtime closed the connection")
		}
		return env
	case <-time.After(wait):
		t.Fatal("no frame from the Runtime")
	}
	return proto.Envelope{}
}

func (p *corePeer) silent(t *testing.T, reason string) {
	t.Helper()
	select {
	case env, ok := <-p.frames:
		if ok {
			t.Fatalf("Runtime sent %s %q %s", env.Type, env.ID, reason)
		}
		t.Fatalf("Runtime closed the connection %s", reason)
	case <-time.After(prototest.SilenceWindow):
	}
}

func dial(t *testing.T, endpoint, version string) (*transport.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	return transport.Dial(ctx, transport.DialOptions{WSURL: endpoint, DeviceID: prototest.DeviceID, Credential: prototest.Credential, DaemonVersion: version})
}

func TestWireRejectsIncompatibleVersions(t *testing.T) {
	peer := newCorePeer(t)
	for _, version := range prototest.IncompatibleVersions() {
		t.Run(version, func(t *testing.T) {
			attempts := 0
			_, err := transport.Reconnect(t.Context(), func(context.Context) (*transport.Conn, error) {
				attempts++
				return dial(t, peer.endpoint, version)
			}, transport.DefaultBackoff, nil)
			if !errors.Is(err, transport.ErrIncompatibleVersion) || !errors.Is(err, transport.ErrPermanent) || attempts != 1 {
				t.Fatalf("mismatch must stop after one attempt: attempts=%d error=%v", attempts, err)
			}
		})
	}
}

// runtimeSide is the production transport and dispatcher with a controlled adapter.
type runtimeSide struct {
	conn        *transport.Conn
	executor    *controlledExecutor
	turn        *controlledTurn
	started     int32
	stopped     chan struct{}
	shutdownErr error
}

func connectRuntime(t *testing.T, peer *corePeer, setupErr error) *runtimeSide {
	t.Helper()
	conn, err := dial(t, peer.endpoint, proto.Version)
	if err != nil {
		t.Fatal(err)
	}
	peer.accept(t)
	rt := &runtimeSide{conn: conn, executor: &controlledExecutor{turn: make(chan *controlledTurn, 1)}, stopped: make(chan struct{})}
	kinds := agent.NewRegistry()
	kinds.RegisterKind(proto.SupportedAgentKind{Kind: prototest.HarnessKind, Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})},
		harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, errors.New("prepared execution must not use prompt_request")
		})
	kinds.RegisterExecutor(prototest.HarnessKind, func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
		return rt.executor, setupErr
	})
	router, err := dispatch.New(dispatch.Config{Registry: kinds, Sender: conn})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for env := range conn.Recv() {
			if err := router.Handle(t.Context(), env); err != nil {
				t.Errorf("dispatch: %v", err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()
		rt.shutdownErr = router.Shutdown(ctx)
		close(rt.stopped)
	}()
	t.Cleanup(func() {
		if rt.turn == nil {
			select {
			case rt.turn = <-rt.executor.turn:
			default:
			}
		}
		if rt.turn != nil {
			rt.turn.release()
		}
		conn.Close()
		select {
		case <-rt.stopped:
			if rt.shutdownErr != nil {
				t.Error(rt.shutdownErr)
			}
		case <-time.After(wait + time.Second):
			t.Error("Runtime shutdown did not complete")
		}
	})
	return rt
}

func TestWireScenarios(t *testing.T) {
	for _, scenario := range prototest.WireScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			var setupErr error
			if scenario.NativeSetupFails {
				setupErr = errors.New("native setup failed")
			}
			peer := newCorePeer(t)
			rt := connectRuntime(t, peer, setupErr)
			for _, step := range scenario.Steps {
				switch step.Action {
				case prototest.Send:
					if step.From == prototest.Core {
						frame, err := peer.bindings.Resolve(step.Frame)
						if err != nil {
							t.Fatal(err)
						}
						if err := peer.ws.WriteJSON(frame); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := peer.bindings.Match(step.Frame, peer.receive(t)); err != nil {
							t.Fatal(err)
						}
						rt.reported(t, step.Frame)
					}
				case prototest.Silence:
					// Core's silence needs nothing from the Runtime.
					if step.From == prototest.Runtime {
						peer.silent(t, "after its last scenario frame")
					}
				case prototest.Settle:
					rt.settle(t, peer)
				case prototest.Disconnect:
					rt.disconnect(t, peer)
				case prototest.Reconnect:
					conn, err := dial(t, peer.endpoint, proto.Version)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { conn.Close() })
					peer.accept(t)
				}
			}
			if starts := rt.executor.starts.Load(); starts != rt.started {
				t.Fatalf("native input submitted %d times for %d started Turns", starts, rt.started)
			}
		})
	}
}

// reported checks the native state behind a preparation status the Runtime sent.
func (rt *runtimeSide) reported(t *testing.T, frame proto.Envelope) {
	t.Helper()
	if frame.Type != proto.TypePreparationStatus {
		return
	}
	var status proto.PreparationStatusPayload
	if err := frame.DecodePayload(&status); err != nil {
		t.Fatal(err)
	}
	switch status.State {
	case "preparing", "ready":
		if rt.executor.starts.Load() != rt.started {
			t.Fatal("preparation submitted input")
		}
	case "failed":
		if rt.executor.starts.Load() != rt.started || rt.executor.closes.Load() != 1 {
			t.Fatalf("failed preparation retained resources or started input: starts=%d closes=%d", rt.executor.starts.Load(), rt.executor.closes.Load())
		}
	case "started":
		select {
		case rt.turn = <-rt.executor.turn:
			rt.started++
		case <-time.After(wait):
			t.Fatal("no native turn")
		}
	}
}

// settle holds native cancellation until the Runtime has stayed silent.
func (rt *runtimeSide) settle(t *testing.T, peer *corePeer) {
	t.Helper()
	select {
	case <-rt.turn.cancelling:
	case <-time.After(wait):
		t.Fatal("cancellation was not received")
	}
	peer.silent(t, "before native settlement")
	rt.turn.release()
}

// disconnect loses the connection after work started; cleanup still settles.
func (rt *runtimeSide) disconnect(t *testing.T, peer *corePeer) {
	t.Helper()
	if rt.turn != nil {
		rt.turn.release()
	}
	rt.conn.Close()
	select {
	case <-rt.stopped:
		if rt.shutdownErr != nil || rt.executor.closes.Load() != 1 {
			t.Fatalf("disconnect cleanup: %v, closes=%d", rt.shutdownErr, rt.executor.closes.Load())
		}
	case <-time.After(wait):
		t.Fatal("disconnect cleanup did not settle")
	}
	select {
	case env, ok := <-peer.frames:
		if ok {
			t.Fatalf("lost connection carried %s %q", env.Type, env.ID)
		}
	case <-time.After(wait):
		t.Fatal("lost connection stayed open")
	}
}

type controlledExecutor struct {
	turn   chan *controlledTurn
	starts atomic.Int32
	closes atomic.Int32
}

func (e *controlledExecutor) Close(context.Context) error { e.closes.Add(1); return nil }
func (e *controlledExecutor) StartTurn(_ context.Context, id string, _ proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	e.starts.Add(1)
	turn := &controlledTurn{id: id, out: out, cancelling: make(chan struct{}), allowCancel: make(chan struct{}), settled: make(chan struct{})}
	e.turn <- turn
	return turn, nil
}

type controlledTurn struct {
	id                                  string
	out                                 chan<- proto.Envelope
	cancelling, allowCancel, settled    chan struct{}
	cancelOnce, finishOnce, releaseOnce sync.Once
}

// release lets a pending native cancellation settle.
func (turn *controlledTurn) release() { turn.releaseOnce.Do(func() { close(turn.allowCancel) }) }

func (turn *controlledTurn) Cancel(ctx context.Context) error {
	turn.cancelOnce.Do(func() { close(turn.cancelling) })
	select {
	case <-turn.allowCancel:
		turn.finishOnce.Do(func() { close(turn.out); close(turn.settled) })
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (turn *controlledTurn) CancellationOutcome() proto.DonePayload {
	return prototest.CancellationOutcome()
}
func (turn *controlledTurn) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	select {
	case <-turn.settled:
		return agent.TurnSettlement{Reason: "cancelled fixture"}, nil
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

// These fixtures exercise settlement only; active input is deliberately rejected.
func (*controlledTurn) SteerWithReceipt(context.Context, proto.PromptSteerPayload, func()) error {
	return agent.ErrSteeringRejected
}
