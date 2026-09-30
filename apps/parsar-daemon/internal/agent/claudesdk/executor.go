package claudesdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type executor struct {
	mu       sync.Mutex
	base     *session
	start    startRequest
	active   *session
	ready    chan error
	done     chan struct{}
	invalid  bool
	nativeID string
}

func NewExecutorFactory(config Config) agent.ExecutorFactory {
	config.Env = slices.Clone(config.Env)
	if config.Workspace != nil {
		workspace := *config.Workspace
		config.Workspace = &workspace
	}
	checked := &runtimeCheckCache{}
	return func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if req.RunID != "" || len(req.Input) != 0 || req.ConversationID != "" {
			return nil, errors.New("claudesdk: Executor preparation cannot submit input")
		}
		start, env, err := prepareConfiguration(config, req)
		if err != nil {
			return nil, err
		}
		info, err := checked.check(ctx, config)
		if err != nil {
			return nil, err
		}
		if err = validateExecutorFeatures(info, start); err != nil {
			return nil, err
		}
		start.Type = "executor_prepare"
		base, err := launch(ctx, config, start, env)
		if err != nil {
			return nil, err
		}
		base.reads.supported = slices.Contains(info.Features, "workspace_read")
		base.directories.supported = slices.Contains(info.Features, "workspace_directory")
		e := &executor{base: base, start: start, ready: make(chan error, 1), done: make(chan struct{}), nativeID: start.Resume}
		go e.read()
		if err = e.write(start); err == nil {
			select {
			case err = <-e.ready:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if closeErr := e.Close(closeCtx); closeErr != nil {
				return e, errors.Join(err, closeErr)
			}
			return nil, err
		}
		return e, nil
	}
}

func validateExecutorFeatures(info RuntimeInfo, start startRequest) error {
	if info.Protocol != 3 {
		return errors.New("claudesdk: Executor bridge protocol is unavailable")
	}
	if start.Workspace != nil && !info.supportsWorkspacePreparation() {
		return errors.New("claudesdk: workspace preparation is unavailable")
	}
	if start.OutputFormat != nil && (!info.SupportsStructuredOutput() || start.Workspace != nil && !info.SupportsWorkspaceStructuredOutput()) {
		return errors.New("claudesdk: packaged runtime does not support workspace structured output")
	}
	if start.Subagents != nil && !info.SupportsSubagents() {
		return errors.New("claudesdk: subagent resources are unavailable")
	}
	if start.Workspace != nil && start.observeFunctions && !info.supportsWorkspaceCommands() {
		return errors.New("claudesdk: packaged runtime does not support workspace command observations")
	}
	if start.Workspace != nil && len(start.Functions) > 0 && !info.SupportsWorkspaceFunctions() {
		return errors.New("claudesdk: workspace functions are unavailable")
	}
	if servers := start.declaredMCP(); len(servers) > 0 {
		for _, server := range servers {
			if start.Workspace != nil && server.ServerURL != "" && !info.SupportsWorkspaceMCP() {
				return errors.New("claudesdk: workspace HTTP MCP is unavailable")
			}
		}
		if !info.SupportsHTTPMCP() {
			return errors.New("claudesdk: packaged runtime does not support HTTP MCP")
		}
		for _, server := range servers {
			if server.Required && !info.SupportsHTTPMCPRequired() {
				return errors.New("claudesdk: packaged runtime does not support required HTTP MCP")
			}
			if server.BearerTokenEnvVar != "" && !info.SupportsHTTPMCPBearer() {
				return errors.New("claudesdk: packaged runtime does not support authenticated HTTP MCP")
			}
		}
	}
	return nil
}

func (e *executor) write(value any) error {
	e.base.writeMu.Lock()
	defer e.base.writeMu.Unlock()
	return json.NewEncoder(e.base.process.Stdin).Encode(value)
}

func (e *executor) read() {
	stderrDone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, e.base.process.Stderr); close(stderrDone) }()
	scanner := e.base.bridgeOutput()
	ready := false
	readyFailure := errors.New("claudesdk: Executor readiness failed")
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		var event bridgeEvent
		if json.Unmarshal(raw, &event) != nil {
			e.base.process.Cancel()
			break
		}
		if !ready {
			if event.Type != "executor_ready" || event.Protocol != 3 || event.TurnID != "" {
				if event.Type == "error" {
					readyFailure = bridgeFailure(event.Code)
				}
				e.base.process.Cancel()
				break
			}
			ready = true
			e.ready <- nil
			continue
		}
		e.mu.Lock()
		turn := e.active
		valid := turn != nil && !turn.nativeEnded && event.TurnID == turn.runID
		if valid && event.Type == "turn_settled" {
			turn.nativeEnded = true
		}
		e.mu.Unlock()
		if !valid {
			e.base.process.Cancel()
			break
		}
		select {
		case turn.frames <- raw:
		case <-e.base.process.Context().Done():
		}
	}
	e.base.process.Cancel()
	for scanner.Scan() {
	}
	<-stderrDone
	_ = e.base.process.Wait()
	e.base.stopWorkspaceReads()
	e.base.stopWorkspaceDirectories()
	e.mu.Lock()
	e.invalid = true
	if e.active != nil {
		close(e.active.frames)
	}
	e.mu.Unlock()
	if !ready {
		e.ready <- readyFailure
	}
	close(e.done)
}

func (e *executor) StartTurn(ctx context.Context, run string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(run) == "" || input.Validate() != nil || out == nil || ctx.Err() != nil {
		return nil, errors.New("claudesdk: Turn requires live context, identity, input and output")
	}
	e.mu.Lock()
	if e.invalid || e.active != nil || e.base.process.Context().Err() != nil {
		e.mu.Unlock()
		return nil, errors.New("claudesdk: Executor is unavailable")
	}
	s := &session{owner: e, runID: run, process: e.base.process, writeMu: e.base.writeMu, frames: make(chan []byte, 64), functions: functionState{calls: map[string]*pendingFunction{}}, settled: make(chan struct{}), outputDone: make(chan struct{}), cancelOutput: make(chan struct{})}
	start := e.start
	start.Resume = e.nativeID
	e.active = s
	e.mu.Unlock()
	go s.runTurn(start, out)
	// Once the write is attempted, a lost receipt has an unknown input outcome.
	stopWrite := context.AfterFunc(ctx, s.invalidate)
	err := e.write(struct {
		Type   string             `json:"type"`
		TurnID string             `json:"turn_id"`
		Input  proto.MessageInput `json:"input"`
	}{"turn_start", run, input})
	stopWrite()
	if err != nil {
		s.invalidate()
		return s, errors.New("claudesdk: Turn input delivery is unknown")
	}
	return s, nil
}

func (e *executor) finishTurn(s *session) {
	e.mu.Lock()
	if e.active != s {
		e.mu.Unlock()
		return
	}
	if native, _ := s.outcome.Metadata[proto.DoneMetaAgentSessionID].(string); native != "" {
		if e.nativeID != "" && e.nativeID != native {
			e.invalid = true
		} else {
			e.nativeID = native
		}
	}
	if !s.turnSettlement.Reusable {
		e.invalid = true
	}
	invalid := e.invalid
	if invalid {
		s.turnSettlement.Reusable = false
		if s.turnSettlement.Reason == "" {
			s.turnSettlement.Reason = "executor_invalidated"
		}
	}
	e.active = nil
	e.mu.Unlock()
	if invalid {
		e.base.process.Cancel()
		<-e.done
	}
}

func (e *executor) retire() {
	e.mu.Lock()
	e.invalid = true
	e.mu.Unlock()
	e.base.process.Cancel()
	<-e.done
}

func (e *executor) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	e.invalid = true
	active := e.active
	e.mu.Unlock()
	e.base.process.Cancel()
	select {
	case <-e.done:
		if active != nil {
			select {
			case <-active.outputDone:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-s.outputDone:
		return s.turnSettlement, errors.Join(s.settlementErr, s.outputErr)
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

var _ agent.Executor = (*executor)(nil)
var _ agent.Turn = (*session)(nil)

// A timed-out operation retains its original Turn identity. Its callback cannot
// retire a healthy successor after that operation has otherwise settled.
func (s *session) invalidate() {
	if s.owner == nil {
		s.process.Cancel()
		return
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if s.owner.active == s {
		s.owner.invalid = true
		s.process.Cancel()
	}
}

func (e *executor) ReadWorkspaceFile(ctx context.Context, path string, maxBytes int) (agent.WorkspaceReadResult, error) {
	return e.base.ReadWorkspaceFile(ctx, path, maxBytes)
}
func (e *executor) ListWorkspaceDirectory(ctx context.Context, path string, maxEntries int) (agent.WorkspaceDirectoryResult, error) {
	return e.base.ListWorkspaceDirectory(ctx, path, maxEntries)
}
