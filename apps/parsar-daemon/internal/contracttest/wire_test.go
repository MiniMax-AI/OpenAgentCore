// Package contracttest exercises the shared protocol over an actual WebSocket,
// using the production gateway, transport and dispatcher with a controlled adapter.
package contracttest

import "github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/transport"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
)

const credential = "synthetic-contract-credential"

type credentialStore struct{}

func (credentialStore) GetDeviceCredential(context.Context, string) (device.Credential, bool, error) {
	return device.Credential{ID: "runtime", WorkspaceID: "tenant", Type: gateway.RuntimeTypeAgentDaemon, CredentialHash: device.HashCredential(credential)}, true, nil
}

func newGateway(t *testing.T) (*gateway.Registry, string) {
	t.Helper()
	reg := gateway.NewRegistry()
	handler := gateway.NewHandler(gateway.HandlerConfig{Registry: reg, Authenticator: gateway.NewAuthenticator(credentialStore{})})
	server := httptest.NewServer(http.HandlerFunc(handler.WS))
	t.Cleanup(server.Close)
	return reg, "ws" + strings.TrimPrefix(server.URL, "http")
}

func dial(t *testing.T, endpoint, version string) (*transport.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	return transport.Dial(ctx, transport.DialOptions{WSURL: endpoint, DeviceID: "runtime", Credential: credential, DaemonVersion: version})
}

func TestWireContractRejectsVersionMismatchWithoutReconnect(t *testing.T) {
	_, endpoint := newGateway(t)
	for _, version := range []string{"0.7.0", "0.8.99", "0.8.", proto.Version + "-dev", proto.Version + "+build"} {
		t.Run(version, func(t *testing.T) {
			attempts := 0
			_, err := transport.Reconnect(t.Context(), func(context.Context) (*transport.Conn, error) {
				attempts++
				return dial(t, endpoint, version)
			}, transport.DefaultBackoff, nil)
			if !errors.Is(err, transport.ErrIncompatibleVersion) || !errors.Is(err, transport.ErrPermanent) || attempts != 1 {
				t.Fatalf("mismatch must stop after one attempt: attempts=%d error=%v", attempts, err)
			}
		})
	}
}

type wireFixture struct {
	core        *gateway.Session
	conn        *transport.Conn
	router      *dispatch.Router
	reg         *gateway.Registry
	endpoint    string
	stopped     chan struct{}
	shutdownErr error
}

func connectFixture(t *testing.T, factory agent.ExecutorFactory) *wireFixture {
	t.Helper()
	reg, endpoint := newGateway(t)
	conn, err := dial(t, endpoint, proto.Version)
	if err != nil {
		t.Fatal(err)
	}
	core, err := reg.WaitForDevice(t.Context(), "runtime", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	kinds := agent.NewRegistry()
	kinds.RegisterKind(proto.SupportedAgentKind{Kind: "contract", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})},
		harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, errors.New("prepared execution must not use prompt_request")
		})
	kinds.RegisterExecutor("contract", factory)
	router, err := dispatch.New(dispatch.Config{Registry: kinds, Sender: conn})
	if err != nil {
		t.Fatal(err)
	}
	f := &wireFixture{core: core, conn: conn, router: router, reg: reg, endpoint: endpoint, stopped: make(chan struct{})}
	go func() {
		for env := range conn.Recv() {
			if err := router.Handle(t.Context(), env); err != nil {
				t.Errorf("dispatch: %v", err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		f.shutdownErr = router.Shutdown(ctx)
		close(f.stopped)
	}()
	t.Cleanup(func() {
		conn.Close()
		select {
		case <-f.stopped:
			if f.shutdownErr != nil {
				t.Error(f.shutdownErr)
			}
		case <-time.After(4 * time.Second):
			t.Error("Runtime shutdown did not complete")
		}
		core.Close("test finished")
	})
	return f
}

func (f *wireFixture) send(t *testing.T, kind, id string, payload any) {
	t.Helper()
	env, err := proto.NewEnvelope(kind, id, payload)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := f.core.Send(ctx, env); err != nil {
		t.Fatal(err)
	}
}

func receive(t *testing.T, sub *gateway.Subscription) proto.Envelope {
	t.Helper()
	select {
	case env, ok := <-sub.Events:
		if !ok {
			t.Fatalf("subscription closed: %v", sub.Err())
		}
		return env
	case <-time.After(3 * time.Second):
		t.Fatal("no protocol response")
	}
	return proto.Envelope{}
}

func status(t *testing.T, sub *gateway.Subscription, want string) proto.PreparationStatusPayload {
	t.Helper()
	for {
		env := receive(t, sub)
		var got proto.PreparationStatusPayload
		if env.Type != proto.TypePreparationStatus || env.DecodePayload(&got) != nil {
			t.Fatalf("invalid preparation response: %+v", env)
		}
		if got.State == want {
			return got
		}
		if got.State != "preparing" && got.State != "starting" {
			t.Fatalf("preparation state=%s, want=%s", got.State, want)
		}
	}
}

func (f *wireFixture) prepare(t *testing.T) (*gateway.Subscription, proto.PreparationStatusPayload) {
	t.Helper()
	sub, err := f.core.SubscribePreparation("prepare")
	if err != nil {
		t.Fatal(err)
	}
	f.send(t, proto.TypeExecutionPrepare, "prepare", proto.ExecutionPreparePayload{
		SessionID: "session", Configuration: proto.PromptRequestPayload{AgentKind: "contract", AgentStateKey: "agents-api-session", StrictResume: true, DisableExecutionEnvironment: true},
	})
	return sub, status(t, sub, "ready")
}

type controlledExecutor struct {
	turn   chan *controlledTurn
	starts atomic.Int32
	closes atomic.Int32
}

func newExecutor() *controlledExecutor {
	return &controlledExecutor{turn: make(chan *controlledTurn, 1)}
}
func (e *controlledExecutor) Close(context.Context) error { e.closes.Add(1); return nil }
func (e *controlledExecutor) StartTurn(_ context.Context, id string, _ proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	e.starts.Add(1)
	turn := &controlledTurn{id: id, out: out, cancelling: make(chan struct{}), allowCancel: make(chan struct{}), settled: make(chan struct{})}
	e.turn <- turn
	return turn, nil
}

type controlledTurn struct {
	id                               string
	out                              chan<- proto.Envelope
	cancelling, allowCancel, settled chan struct{}
	cancelOnce, finishOnce           sync.Once
}

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
	return proto.DonePayload{Content: "partial", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-session"}}
}
func (turn *controlledTurn) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	select {
	case <-turn.settled:
		return agent.TurnSettlement{Reason: "cancelled fixture"}, nil
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

func TestWireContractCancellationWaitsForSettlement(t *testing.T) {
	executor := newExecutor()
	f := connectFixture(t, func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) { return executor, nil })
	prepared, ready := f.prepare(t)
	if executor.starts.Load() != 0 {
		t.Fatal("preparation submitted input")
	}
	run, err := f.core.SubscribeDurable("run")
	if err != nil {
		t.Fatal(err)
	}
	f.send(t, proto.TypeExecutionStart, "prepare", proto.ExecutionStartPayload{ExecutorID: ready.ExecutorID, Handle: ready.Handle, RunID: "run", Input: proto.TextInput("hello")})
	started := status(t, prepared, "started")
	if started.RunID != "run" || started.ExecutorID != ready.ExecutorID {
		t.Fatal("start lost execution identity")
	}
	var turn *controlledTurn
	select {
	case turn = <-executor.turn:
	case <-time.After(3 * time.Second):
		t.Fatal("no native turn")
	}
	var unblock sync.Once
	release := func() { unblock.Do(func() { close(turn.allowCancel) }) }
	t.Cleanup(release)
	result := make(chan proto.InteractionDecisionAckPayload, 1)
	ackErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		env, _ := proto.NewEnvelope(proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"})
		ack, err := f.core.SendAndWaitInteractionAck(ctx, env, "cancel")
		result <- ack
		ackErr <- err
	}()
	select {
	case <-turn.cancelling:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation was not received")
	}
	select {
	case <-result:
		t.Fatal("receipt preceded native settlement")
	default:
	}
	release()
	select {
	case ack := <-result:
		if err := <-ackErr; err != nil || !ack.Applied || ack.Outcome == nil || ack.Outcome.Content != "partial" {
			t.Fatalf("invalid cancellation receipt: %+v, %v", ack, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing settled cancellation receipt")
	}
	f.core.Unsubscribe("run")
	for env := range run.Events {
		if env.ID != "run" {
			t.Fatal("settled cancellation output lost Run correlation")
		}
	}
	if executor.starts.Load() != 1 {
		t.Fatal("input was submitted more than once")
	}
}

func TestWireContractPreparationFailureCleansUpWithoutRunCompletion(t *testing.T) {
	executor := newExecutor()
	f := connectFixture(t, func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
		return executor, errors.New("native setup failed")
	})
	sub, err := f.core.SubscribePreparation("prepare")
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.core.SubscribeDurable("run")
	if err != nil {
		t.Fatal(err)
	}
	f.send(t, proto.TypeExecutionPrepare, "prepare", proto.ExecutionPreparePayload{SessionID: "session", Configuration: proto.PromptRequestPayload{AgentKind: "contract", AgentStateKey: "agents-api-session", StrictResume: true, DisableExecutionEnvironment: true}})
	failed := status(t, sub, "failed")
	if failed.ErrorCode != "preparation_failed" || executor.starts.Load() != 0 || executor.closes.Load() != 1 {
		t.Fatalf("failed preparation retained resources or started input: %+v starts=%d closes=%d", failed, executor.starts.Load(), executor.closes.Load())
	}
	if len(run.Events) != 0 {
		t.Fatal("preparation failure manufactured a Run result")
	}
}

func TestWireContractDisconnectIsUnknownAndReconnectDoesNotReplay(t *testing.T) {
	executor := newExecutor()
	f := connectFixture(t, func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) { return executor, nil })
	prepared, ready := f.prepare(t)
	run, err := f.core.SubscribeDurable("run")
	if err != nil {
		t.Fatal(err)
	}
	f.send(t, proto.TypeExecutionStart, "prepare", proto.ExecutionStartPayload{ExecutorID: ready.ExecutorID, Handle: ready.Handle, RunID: "run", Input: proto.TextInput("hello")})
	status(t, prepared, "started")
	var turn *controlledTurn
	select {
	case turn = <-executor.turn:
	case <-time.After(3 * time.Second):
		t.Fatal("no native turn")
	}
	close(turn.allowCancel)
	// Work has started. Losing observation cannot establish its result even
	// when Runtime subsequently settles cleanup.
	f.conn.Close()
	select {
	case <-f.core.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("gateway stayed connected")
	}
	for env := range run.Events {
		if env.Type == proto.TypeError || env.Type == proto.TypeDone {
			t.Fatal("disconnect manufactured execution failure/completion")
		}
	}
	if !errors.Is(run.Err(), gateway.ErrSessionClosed) {
		t.Fatalf("disconnect outcome: %v", run.Err())
	}
	conn, err := dial(t, f.endpoint, proto.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	core, err := f.reg.WaitForDevice(t.Context(), "runtime", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close("test done")
	if core == f.core || f.reg.LookupRun("run") != nil {
		t.Fatal("reconnect inherited stale Run ownership")
	}
	select {
	case env := <-conn.Recv():
		t.Fatalf("reconnect replayed work for old handle %s: %+v", ready.Handle, env)
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-f.stopped:
		if f.shutdownErr != nil || executor.closes.Load() != 1 {
			t.Fatalf("disconnect cleanup: %v, closes=%d", f.shutdownErr, executor.closes.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect cleanup did not settle")
	}
	if executor.starts.Load() != 1 {
		t.Fatal("reconnect replayed input")
	}
}

// These fixtures exercise settlement only; active input is deliberately rejected.
func (*controlledTurn) SteerWithReceipt(context.Context, proto.PromptSteerPayload, func()) error {
	return agent.ErrSteeringRejected
}
