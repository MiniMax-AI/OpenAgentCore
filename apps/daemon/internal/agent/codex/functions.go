package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type dynamicFunctionTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type functionCalls struct {
	mu          sync.Mutex
	definitions []dynamicFunctionTool
	names       map[string]bool
	pending     map[string]*pendingFunction
	closed      bool
}

func prepareFunctionTools(tools []proto.FunctionTool) (*functionCalls, error) {
	state := &functionCalls{names: map[string]bool{}, pending: map[string]*pendingFunction{}}
	for _, tool := range tools {
		var schema map[string]any
		if strings.TrimSpace(tool.Name) == "" || state.names[tool.Name] || json.Unmarshal(tool.Parameters, &schema) != nil || schema == nil {
			return nil, errors.New("function tools require unique names and object schemas")
		}
		state.names[tool.Name] = true
		state.definitions = append(state.definitions, dynamicFunctionTool{Type: "function", Name: tool.Name, Description: tool.Description, InputSchema: append(json.RawMessage(nil), tool.Parameters...)})
	}
	return state, nil
}

func (s *Session) handleFunctionCall(raw json.RawMessage, rpcID any) (any, error) {
	var call struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		CallID    string          `json:"callId"`
		Namespace *string         `json:"namespace"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return nil, err
	}
	root := s.isRootTurn(call.ThreadID, call.TurnID)
	child := !root && s.subagents != nil && call.TurnID != "" && call.ThreadID != "" && !s.isRootThread(call.ThreadID)
	if s.functions == nil || !s.functions.names[call.Tool] || call.Namespace != nil || call.CallID == "" || !(root || child) || !json.Valid(call.Arguments) {
		return nil, errors.New("unexpected function call")
	}
	payload := proto.FunctionCallPayload{CallID: call.CallID, Name: call.Tool, Arguments: call.Arguments}
	if child {
		payload.NativeID, payload.TurnID = call.ThreadID, call.TurnID
	}
	env, err := proto.NewEnvelope(proto.TypeFunctionCall, s.runID, payload)
	if err != nil {
		return nil, err
	}
	s.functions.mu.Lock()
	if s.functions.closed || s.cancelled.Load() || (root && s.terminal.Load()) || s.functions.pending[call.CallID] != nil || len(s.functions.pending) >= 64 {
		s.functions.mu.Unlock()
		return nil, errors.New("function call cannot be admitted")
	}
	pending := &pendingFunction{rpcID: rpcID, thread: call.ThreadID, turnID: call.TurnID, name: call.Tool, receipt: make(chan error, 1)}
	if child {
		pending.held = &env
	}
	s.functions.pending[call.CallID] = pending
	s.functions.mu.Unlock()
	if child {
		select {
		case s.subagents.wake <- struct{}{}:
		default:
		}
		return DeferReply, nil
	}
	if err = s.sendFunctionCall(env); err != nil {
		s.functions.mu.Lock()
		delete(s.functions.pending, call.CallID)
		s.functions.mu.Unlock()
		return nil, err
	}
	return DeferReply, nil
}

func (s *Session) sendFunctionCall(env proto.Envelope) error {
	s.outMu.RLock()
	defer s.outMu.RUnlock()
	if s.outClosed {
		return agent.ErrUnknownFunctionCall
	}
	timer := time.NewTimer(terminalSendTimeout)
	defer timer.Stop()
	select {
	case s.out <- env:
		return nil
	case <-s.cancelCtx.Done():
		return s.cancelCtx.Err()
	case <-timer.C:
		return errors.New("function call delivery timed out")
	}
}

type functionContent struct {
	Type     string  `json:"type"`
	Text     *string `json:"text,omitempty"`
	ImageURL *string `json:"imageUrl,omitempty"`
}

// stopFunctionCalls ends every pending call. With rootOnly it ends the root's
// calls and keeps accepting those of Subagents, which outlive the root Turn.
func (s *Session) stopFunctionCalls(rootOnly bool) {
	if s.functions != nil {
		s.functions.mu.Lock()
		s.functions.closed = s.functions.closed || !rootOnly
		for id, pending := range s.functions.pending {
			if !rootOnly || pending.thread == s.currentThreadID() {
				pending.receipt <- agent.ErrUnknownFunctionCall
				delete(s.functions.pending, id)
			}
		}
		s.functions.mu.Unlock()
	}
}

// withdrawChildCalls removes the pending calls of a child Turn that ended and
// reports them to Core as withdrawn. Native drops their requests without a
// reply, so no result can be applied. A call still held was never reported.
func (s *Session) withdrawChildCalls(ctx context.Context, thread, turn string) error {
	if s.functions == nil {
		return nil
	}
	var withdrawn []proto.Envelope
	s.functions.mu.Lock()
	for id, pending := range s.functions.pending {
		if pending.thread != thread || pending.turnID != turn {
			continue
		}
		delete(s.functions.pending, id)
		pending.receipt <- agent.ErrUnknownFunctionCall
		if pending.held != nil {
			continue
		}
		env, err := proto.NewEnvelope(proto.TypeFunctionCallWithdrawn, s.runID, proto.FunctionCallWithdrawnPayload{CallID: id, NativeID: thread, TurnID: turn})
		if err != nil {
			s.functions.mu.Unlock()
			return err
		}
		withdrawn = append(withdrawn, env)
	}
	s.functions.mu.Unlock()
	for _, env := range withdrawn {
		if !s.sendWithin(ctx, env) {
			return errors.New("codex: function call withdrawal delivery unavailable")
		}
	}
	return nil
}

// sendHeldFunctionCalls sends the Subagent calls whose child Turn Core already
// has, because Core resolves a child's call through its identity.
func (s *Session) sendHeldFunctionCalls(ctx context.Context) {
	if s.functions == nil {
		return
	}
	var ready []proto.Envelope
	s.functions.mu.Lock()
	for _, pending := range s.functions.pending {
		if _, sent := s.subagents.sent[proto.TypeSubagentTurn+":"+pending.thread+":"+pending.turnID]; sent && pending.held != nil {
			ready = append(ready, *pending.held)
			pending.held = nil
		}
	}
	s.functions.mu.Unlock()
	for _, env := range ready {
		s.sendWithin(ctx, env)
	}
}
