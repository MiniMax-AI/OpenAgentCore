// This synthetic harness exercises the production router without a native model.
// It is test-only, has no workspace/tools, and is never in the built-in catalog.
package main

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

type sender struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func (s *sender) Send(_ context.Context, e proto.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.encoder.Encode(e)
}

type harness struct {
	mu      sync.Mutex
	history map[string]string
}

func (h *harness) prepare(_ context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
	if req.RunID != "" || len(req.Input) != 0 || req.ConversationID != "" {
		return nil, errors.New("preparation submitted fixture input")
	}
	if !req.StrictResume || !req.DisableExecutionEnvironment || !req.DisableSubagents || len(req.FunctionTools) > 0 || req.MCPHTTPServers != nil {
		return nil, errors.New("unsupported fixture operation")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	previous := h.history[req.AgentStateKey]
	if req.AgentSessionID != previous || (req.RequireExistingNativeSession && previous == "") {
		return nil, errors.New("native history mismatch")
	}
	if previous == "" {
		previous = "fixture-" + req.AgentStateKey
		h.history[req.AgentStateKey] = previous
	}
	return &executor{native: previous}, nil
}

type executor struct {
	mu     sync.Mutex
	native string
	active *session
	closed bool
}

func (e *executor) StartTurn(ctx context.Context, run string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	if ctx.Err() != nil || run == "" || out == nil {
		return nil, errors.New("invalid fixture Start")
	}
	if _, err := input.TextOnly(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.active != nil {
		return nil, errors.New("fixture Executor unavailable")
	}
	s := &session{out: out, run: run, native: e.native, settled: make(chan struct{})}
	s.release = func() {
		e.mu.Lock()
		if e.active == s {
			e.active = nil
		}
		e.mu.Unlock()
	}
	e.active = s
	s.emit(proto.TypeDelta, proto.DeltaPayload{Delta: "ready", Sequence: 1})
	return s, nil
}

func (e *executor) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	current := e.active
	e.mu.Unlock()
	if current != nil {
		return current.Cancel(ctx)
	}
	return nil
}

type session struct {
	mu          sync.Mutex
	out         chan<- proto.Envelope
	run, native string
	closed      bool
	settled     chan struct{}
	release     func()
}

func (s *session) emit(kind string, payload any) {
	e, _ := proto.NewEnvelope(kind, s.run, payload)
	s.out <- e
}
func (s *session) Cancel(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.out)
		s.release()
		close(s.settled)
	}
	return nil
}
func (s *session) CancellationOutcome() proto.DonePayload {
	return proto.DonePayload{Content: "cancelled", Metadata: map[string]any{proto.DoneMetaAgentSessionID: s.native}}
}
func (s *session) Steer(ctx context.Context, p proto.PromptSteerPayload) error {
	return s.SteerWithReceipt(ctx, p, func() {})
}
func (s *session) SteerWithReceipt(_ context.Context, p proto.PromptSteerPayload, written func()) error {
	text, err := p.Input.TextOnly()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return agent.ErrSteeringInactive
	}
	written()
	s.emit(proto.TypeDelta, proto.DeltaPayload{Delta: text, Sequence: 2})
	s.emit(proto.TypeDone, proto.DonePayload{Content: "ready" + text, Metadata: map[string]any{proto.DoneMetaAgentSessionID: s.native}})
	s.closed = true
	close(s.out)
	s.release()
	close(s.settled)
	return nil
}

func (s *session) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	select {
	case <-s.settled:
		return agent.TurnSettlement{Reusable: true}, nil
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

func run() error {
	registry := agent.NewRegistry()
	h := &harness{history: map[string]string{}}
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "fixture_harness", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
		Streaming: proto.CapabilitySupported, Steering: proto.CapabilitySupported, DurableTurns: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported, ExecutionControls: proto.CapabilitySupported, ToolObservations: proto.CapabilitySupported, SubagentControl: proto.CapabilitySupported, EnvironmentNone: proto.CapabilitySupported,
	})}, harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		return nil, errors.New("fixture execution requires an Executor")
	})
	registry.RegisterExecutor("fixture_harness", h.prepare)
	sink := &sender{encoder: json.NewEncoder(os.Stdout)}
	router, err := dispatch.New(dispatch.Config{Registry: registry, Sender: sink, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		return err
	}
	defer router.Shutdown(context.Background())
	heartbeat, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: registry.SupportedAgentKinds()})
	if err = sink.Send(context.Background(), heartbeat); err != nil {
		return err
	}
	decoder := json.NewDecoder(os.Stdin)
	for {
		var e proto.Envelope
		if err = decoder.Decode(&e); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		if err = router.Handle(context.Background(), e); err != nil {
			return err
		}
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
