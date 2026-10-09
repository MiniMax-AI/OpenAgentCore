package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type Session struct {
	ctx  context.Context
	req  proto.PromptRequestPayload
	opts launchOptions
	*connection
	executor             *executor
	settlement           agent.TurnSettlement
	settlementErr        error
	settled              chan struct{}
	inputDone            chan struct{}
	outputCancel         context.CancelFunc
	operations           sync.WaitGroup
	closing              bool
	cancelled            bool
	inputUncertain       bool
	out                  chan<- proto.Envelope
	frames               chan rpcFrame
	finished             chan struct{}
	mu                   sync.Mutex
	runID                string
	sessionID            string
	nativeModel          string
	outputContext        context.Context
	outcome              proto.DonePayload
	steeringReady        bool
	steeringTurn         string
	sequence             uint64
	active               bool
	message              string
	messageText          strings.Builder
	completedMessages    map[string]bool
	tools                map[string]toolUpdate
	completedTools       map[string]bool
	previousNativeTurns  map[string]bool
	rootCompletedAtMS    *int64
	subagentHistoryReady bool
	// These cancellation identities are protected by mu; tool projection stays
	// on the reader. A local failure does not remove a captured remote owner.
	mcpCalls     map[string]string
	cancelledMCP map[string]bool
}

func launch(ctx context.Context, req proto.PromptRequestPayload, opts launchOptions, binary string) (*Session, error) {
	process, err := opts.start(clirunner.StartOptions{Parent: ctx, Binary: binary, Args: []string{opts.script, "acp"}, Dir: opts.Dir, Env: opts.Env, NeedStdin: true})
	if err != nil {
		return nil, err
	}
	c := &connection{process: process, exited: make(chan struct{}), responses: map[string]chan rpcFrame{}}
	s := newTurnSession(ctx, req, opts, c, nil)
	c.current = s
	go c.read()
	return s, nil
}

func newTurnSession(ctx context.Context, req proto.PromptRequestPayload, opts launchOptions, c *connection, out chan<- proto.Envelope) *Session {
	return &Session{ctx: ctx, req: req, opts: opts, connection: c, out: out, frames: make(chan rpcFrame, 32), finished: make(chan struct{}), tools: map[string]toolUpdate{}, completedTools: map[string]bool{}, completedMessages: map[string]bool{}}
}

func (s *Session) prepareNative() error {
	var initialized struct {
		ProtocolVersion int `json:"protocolVersion"`
		Meta            struct {
			MCPLifecycle struct{ Version int } `json:"oac/mcp-lifecycle"`
			Subagents    struct {
				Version, MaxConcurrent int
				WorkspaceTools         string
			} `json:"oac/subagents"`
		} `json:"_meta"`
	}
	if err := s.call("initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]string{"name": "oac", "version": "1"}}, &initialized, false); err != nil {
		return err
	}
	if initialized.ProtocolVersion != 1 {
		return fmt.Errorf("mcode: unsupported ACP protocol version %d", initialized.ProtocolVersion)
	}
	for _, binding := range s.opts.bindings {
		if binding.Transport == "stdio" && initialized.Meta.MCPLifecycle.Version != 2 {
			return fmt.Errorf("mcode: native MCP lifecycle is unavailable")
		}
	}
	if !s.req.DisableSubagents && (initialized.Meta.Subagents.Version != 1 || initialized.Meta.Subagents.WorkspaceTools != "protected-mcp-v1" || s.req.MaxConcurrentSubagents == nil || initialized.Meta.Subagents.MaxConcurrent != *s.req.MaxConcurrentSubagents) {
		return fmt.Errorf("mcode: native Subagent admission is unavailable")
	}
	params := map[string]any{"cwd": s.opts.Dir, "mcpServers": s.opts.MCP}
	method := "session/new"
	if s.req.AgentSessionID != "" {
		method = "session/load"
		params["sessionId"] = s.req.AgentSessionID
	}
	var session sessionResult
	if err := s.call(method, params, &session, false); err != nil {
		return err
	}
	if s.req.AgentSessionID != "" {
		session.SessionID = s.req.AgentSessionID
	}
	if session.SessionID == "" {
		return fmt.Errorf("mcode: ACP returned an empty session id")
	}
	s.mu.Lock()
	s.sessionID = session.SessionID
	s.mu.Unlock()
	model, err := advertisedModel(session.ConfigOptions, s.opts.Model)
	if err != nil && s.req.AgentSessionID != "" && !slices.ContainsFunc(session.ConfigOptions, func(option configOption) bool { return option.ID == "model" }) {
		// Native load omits the selector when its persisted model was removed; selection still validates against the current catalog.
		model = "m:custom_provider%3Aoac:" + strings.ReplaceAll(url.QueryEscape(s.opts.Model), "+", "%20") + ":v:"
		err = nil
	}
	if err != nil {
		return err
	}
	s.nativeModel = model
	if err := s.call("session/set_config_option", map[string]any{"sessionId": session.SessionID, "configId": "model", "value": model}, nil, false); err != nil {
		return err
	}
	return nil
}

func (s *Session) executePrompt(prompt string) error {
	s.active = true
	var result struct {
		StopReason string `json:"stopReason"`
	}
	err := s.call("session/prompt", map[string]any{"sessionId": s.sessionID, "prompt": promptContent(prompt)}, &result, true)
	s.active = false
	s.mu.Lock()
	s.steeringReady = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.mu.Lock()
	cancelled := s.cancelled
	s.mu.Unlock()
	if result.StopReason == "cancelled" && cancelled {
		return nil
	}
	if result.StopReason != "end_turn" {
		return fmt.Errorf("mcode: prompt stopped (%s)", result.StopReason)
	}
	s.completeMessage()
	return nil
}

// Select the advertised custom model, rather than using mcode's native default.
func advertisedModel(options []configOption, model string) (string, error) {
	for _, option := range options {
		if option.ID != "model" {
			continue
		}
		for _, candidate := range option.Options {
			parts := strings.Split(candidate.Value, ":")
			if len(parts) < 4 || parts[0] != "m" {
				continue
			}
			provider, err := decodeComponent(parts[1])
			if err != nil {
				continue
			}
			id, err := decodeComponent(parts[2])
			if err != nil {
				continue
			}
			if provider == "custom_provider:oac" && id == model && (parts[3] == "u" || (len(parts) == 5 && parts[3] == "v" && parts[4] == "")) {
				return candidate.Value, nil
			}
		}
	}
	return "", fmt.Errorf("mcode: configured model is not advertised by the CLI")
}

func (s *Session) call(method string, params any, result any, prompt bool) error {
	id, _ := s.reserveResponse()
	s.removeResponse(id)
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if prompt {
		// Serialize the admission check with the wire, but release the owner
		// mutex before a pipe write so cancellation and Close can stop it.
		s.connection.writeMu.Lock()
		s.executor.mu.Lock()
		s.mu.Lock()
		cancelled := s.cancelled
		s.mu.Unlock()
		if cancelled || s.executor.closed {
			s.executor.mu.Unlock()
			s.connection.writeMu.Unlock()
			return errTurnCancelled
		}
		s.executor.mu.Unlock()
		err = json.NewEncoder(s.process.Stdin).Encode(rpcFrame{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: raw})
		s.connection.writeMu.Unlock()
	} else {
		err = s.write(rpcFrame{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: raw})
	}
	if err != nil {
		return err
	}
	ctx := s.process.Context()
	if !prompt {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("mcode: %s: %w", method, ctx.Err())
		case <-s.exited:
			return fmt.Errorf("mcode: ACP process exited: %v", s.exitErr)
		case frame, ok := <-s.frames:
			if !ok {
				<-s.exited
				return fmt.Errorf("mcode: ACP process exited: %v", s.exitErr)
			}
			if frame.Method != "" {
				if err := s.handle(frame); err != nil {
					return err
				}
				continue
			}
			if string(frame.ID) != id {
				continue
			}
			if frame.Error != nil {
				return fmt.Errorf("mcode: %s: %s", method, frame.Error.Message)
			}
			if result != nil {
				if err := json.Unmarshal(frame.Result, result); err != nil {
					return fmt.Errorf("mcode: invalid %s result", method)
				}
			}
			return nil
		}
	}
}

func (s *Session) emit(kind string, payload any) {
	env, err := proto.NewEnvelope(kind, s.runID, payload)
	if err != nil {
		return
	}
	select {
	case s.out <- env:
		return
	default:
	}
	select {
	case s.out <- env:
	case <-s.outputContext.Done():
	}
}
