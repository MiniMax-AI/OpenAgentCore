package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	configuration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/pi"
)

// prepareTimeout bounds the start-up round trip that proves the child speaks
// the RPC protocol before any Session work is accepted.
const prepareTimeout = 15 * time.Second

// turnTools is the explicit tool selection. Pi enables read/bash/edit/write by
// default; grep, find and ls exist but stay off unless listed.
const turnTools = "read,bash,edit,write,grep,find,ls"

type executor struct {
	mu        sync.Mutex
	sessionID string
	rpc       *rpcProcess
	req       proto.PromptRequestPayload
	active    *Session
	cursor    string
	closed    bool
}

var _ agent.Executor = (*executor)(nil)

// NewExecutorFactory prepares one Pi process per Session. StateRoot holds
// retained native history; RuntimeRoot holds instance-private configuration.
func NewExecutorFactory(stateRoot, runtimeRoot string) agent.ExecutorFactory {
	return func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
		return prepare(ctx, stateRoot, runtimeRoot, req)
	}
}

func prepare(ctx context.Context, stateRoot, runtimeRoot string, req proto.PromptRequestPayload) (*executor, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.RunID != "" || len(req.Input) != 0 {
		return nil, fmt.Errorf("pi: Executor configuration cannot contain Turn input")
	}
	if err := req.ValidateAgentOptions(); err != nil {
		return nil, err
	}
	prepared, err := configuration.Configuration().Prepare(req.AgentOptions)
	if err != nil {
		return nil, err
	}
	if prepared.Model == "" || prepared.Provider == nil {
		return nil, fmt.Errorf("pi: model and model_provider are required")
	}
	if req.LocalEnvironment == nil || strings.TrimSpace(req.LocalEnvironment.WorkspaceRoot) == "" {
		return nil, fmt.Errorf("pi: a workspace directory is required")
	}
	// Each Session owns its private agent directory and home: two concurrent
	// Sessions with different frozen models must not share models.json.
	sessionRoot := filepath.Join(runtimeRoot, "agents", sessionDirectoryName(req.AgentStateKey))
	agentDir := filepath.Join(sessionRoot, "agent")
	if err := writeModelConfig(agentDir, prepared); err != nil {
		return nil, err
	}
	home := filepath.Join(sessionRoot, "home")
	sessionDir := filepath.Join(stateRoot, "sessions")
	for _, dir := range []string{home, sessionDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("pi: prepare directory: %w", err)
		}
	}
	sessionID := req.AgentSessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	args := []string{
		"--mode", "rpc",
		"--session-id", sessionID,
		"--provider", providerID,
		"--model", prepared.Model,
		"--session-dir", sessionDir,
		"--tools", turnTools,
		"--no-approve",
		"--no-extensions",
		"--no-mcp",
		"--no-skills",
		"--no-prompt-templates",
		"--no-themes",
		"--no-context-files",
	}
	if level, ok := prepared.HarnessConfig["thinking_level"].(string); ok {
		args = append(args, "--thinking", level)
	}
	if prompt, ok := req.AgentOptions["system_prompt"].(string); ok && prompt != "" {
		path := filepath.Join(sessionRoot, "system-prompt.md")
		if err := os.WriteFile(path, []byte(prompt), 0o600); err != nil {
			return nil, fmt.Errorf("pi: write system prompt: %w", err)
		}
		args = append(args, "--system-prompt", path)
	}
	process, err := startRPC(ctx, StartOptions{
		Binary: defaultBinary(),
		Args:   args,
		Dir:    req.LocalEnvironment.WorkspaceRoot,
		Env: []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + home,
			"PI_CODING_AGENT_DIR=" + agentDir,
			modelKeyEnv + "=" + prepared.Provider.APIKey,
		},
	})
	if err != nil {
		return nil, err
	}
	e := &executor{rpc: process, req: req, sessionID: sessionID}
	go e.pump()
	probeCtx, cancel := context.WithTimeout(ctx, prepareTimeout)
	defer cancel()
	if _, err := process.call(probeCtx, "get_state", nil); err != nil {
		_ = process.Close()
		return nil, fmt.Errorf("pi: prepare: %w", err)
	}
	data, err := process.call(probeCtx, "get_entries", nil)
	if err != nil {
		_ = process.Close()
		return nil, fmt.Errorf("pi: prepare entries: %w", err)
	}
	var page entriesPage
	_ = json.Unmarshal(data, &page)
	if req.RequireExistingNativeSession && len(page.Entries) == 0 {
		// Pi creates a missing session on `--session-id`; never let recovery
		// silently start a new root when existing history was required.
		_ = process.Close()
		return nil, fmt.Errorf("pi: required native session history is missing")
	}
	e.cursor = entriesLeaf(data)
	return e, nil
}

// pump routes process events to the single active Turn. The Runtime serializes
// Turns per Executor, so a stale Turn can never receive a successor's events.
func (e *executor) pump() {
	for event := range e.rpc.Events() {
		e.mu.Lock()
		active := e.active
		e.mu.Unlock()
		if active != nil {
			active.handle(event)
		}
	}
	e.mu.Lock()
	active := e.active
	e.mu.Unlock()
	if active != nil {
		active.fail(fmt.Errorf("pi: process closed: %v", e.rpc.Stderr()))
	}
}

func (e *executor) StartTurn(ctx context.Context, runID string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	if out == nil {
		return nil, fmt.Errorf("pi: output channel required")
	}
	text, err := input.TextOnly()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("pi: executor closed")
	}
	if e.active != nil {
		select {
		case <-e.active.settled:
			e.active = nil
		default:
			e.mu.Unlock()
			return nil, fmt.Errorf("pi: a Turn is already active")
		}
	}
	t := newSession(runID, out, e.rpc, e.cursor, e.sessionID, e.req.ObserveMessages)
	e.active = t
	e.mu.Unlock()

	if _, err := e.rpc.call(ctx, "prompt", map[string]any{"message": text}); err != nil {
		if errors.Is(err, errRPCTimeout) || errors.Is(err, errRPCClosed) {
			// Submission may have happened; the Turn keeps ownership of out and
			// the caller must settle instead of replaying the input.
			t.fail(err)
			return t, err
		}
		e.mu.Lock()
		e.active = nil
		e.mu.Unlock()
		return nil, err
	}
	return t, nil
}

func (e *executor) Close(ctx context.Context) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	active := e.active
	e.active = nil
	e.mu.Unlock()
	if active != nil {
		active.fail(fmt.Errorf("pi: executor closed"))
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			_ = e.rpc.Close()
			return ctx.Err()
		default:
		}
	}
	return e.rpc.Close()
}

func (e *executor) ReadWorkspaceFile(context.Context, string, int) (agent.WorkspaceReadResult, error) {
	return agent.WorkspaceReadResult{}, unsupported("workspace_read")
}

func (e *executor) ListWorkspaceDirectory(context.Context, string, int) (agent.WorkspaceDirectoryResult, error) {
	return agent.WorkspaceDirectoryResult{}, unsupported("workspace_list")
}

func (e *executor) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, unsupported("workspace_write")
}

// sessionDirectoryName converts the Runtime state key into one path component.
func sessionDirectoryName(key string) string {
	if key == "" {
		key = "session"
	}
	var name strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			name.WriteRune(r)
		default:
			name.WriteByte('-')
		}
	}
	value := strings.Trim(name.String(), ".")
	if value == "" {
		value = "session"
	}
	if len(value) > 96 {
		value = value[:96]
	}
	return value
}
