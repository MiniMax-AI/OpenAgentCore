package mcode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

var errTurnCancelled = errors.New("mcode: Turn cancelled before submission")

type executor struct {
	mu                        sync.Mutex
	connection                *connection
	req                       proto.PromptRequestPayload
	opts                      launchOptions
	nativeSession, model      string
	active                    *Session
	starting, closed, invalid bool
}

var _ agent.Executor = (*executor)(nil)
var _ agent.Turn = (*Session)(nil)

// NewExecutorFactory fixes the deployment workspace once; nil selects none.
func NewExecutorFactory(config *WorkspaceConfig) agent.ExecutorFactory {
	var frozen *WorkspaceConfig
	if config != nil {
		value := *config
		value.AllowedDomains = append([]string(nil), config.AllowedDomains...)
		frozen = &value
	}
	return func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if req.RunID != "" || len(req.Input) != 0 || req.ConversationID != "" {
			return nil, fmt.Errorf("mcode: Executor configuration cannot contain Turn input")
		}
		var err error
		var opts launchOptions
		binary := defaultBinary()
		if frozen == nil {
			opts, err = prepareOptions(ctx, req)
		} else {
			binary = frozen.Binary
			opts, err = prepareWorkspaceOptions(ctx, *frozen, req)
		}
		if err != nil {
			return nil, err
		}
		resource, err := newExecutor(ctx, req, opts, binary)
		if resource == nil {
			return nil, err
		}
		return resource, err
	}
}

func newExecutor(ctx context.Context, req proto.PromptRequestPayload, opts launchOptions, binary string) (*executor, error) {
	bootstrap, err := launch(ctx, req, opts, binary, nil)
	if err != nil {
		return nil, err
	}
	e := &executor{connection: bootstrap.connection, req: req, opts: opts}
	err = bootstrap.prepareNative()
	e.connection.setCurrent(nil)
	close(bootstrap.finished)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := e.Close(cleanup); closeErr != nil {
			return e, err
		}
		return nil, err
	}
	e.nativeSession, e.model = bootstrap.sessionID, bootstrap.nativeModel
	return e, nil
}

func (e *executor) StartTurn(ctx context.Context, runID string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(runID) == "" || input.Validate() != nil || out == nil || ctx.Err() != nil {
		return nil, fmt.Errorf("mcode: Turn requires live context, identity, input and output")
	}
	text, err := input.TextOnly()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	if e.closed || e.invalid || e.starting || e.active != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("mcode: Executor is unavailable")
	}
	e.starting = true
	e.mu.Unlock()

	// No model input is sent and no output channel retained until this fence.
	barrier, cancel := context.WithTimeout(ctx, 60*time.Second)
	err = e.connection.barrier(barrier, e.nativeSession, e.model)
	cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.starting = false
	if err != nil || e.closed || ctx.Err() != nil {
		if err != nil {
			e.invalid = true
			return nil, err
		}
		return nil, fmt.Errorf("mcode: Executor closed before input")
	}
	select {
	case <-e.connection.exited:
		e.invalid = true
		return nil, fmt.Errorf("mcode: native process exited")
	default:
	}
	req := e.req
	req.RunID, req.Input = runID, proto.TextInput(text)
	s := newTurnSession(e.connection.process.Context(), req, e.opts, e.connection, out)
	s.executor, s.sessionID, s.nativeModel = e, e.nativeSession, e.model
	s.settled, s.inputDone = make(chan struct{}), make(chan struct{})
	s.outputContext, s.outputCancel = context.WithCancel(context.Background())
	e.active = s
	e.connection.setCurrent(s)
	go s.runExecutorTurn()
	go func() {
		select {
		case <-ctx.Done():
			cancellation, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			_ = s.Cancel(cancellation)
		case <-s.settled:
		}
	}()
	return s, nil
}

// Close keeps the native owner reachable until process, pipes and active Turn
// settlement all finish. Repeating it waits on those same owners.
func (e *executor) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	e.closed = true
	current := e.active
	if current != nil {
		current.outputCancel()
	}
	e.connection.process.Cancel()
	e.mu.Unlock()
	select {
	case <-e.connection.exited:
	case <-ctx.Done():
		return ctx.Err()
	}
	if current != nil {
		select {
		case <-current.settled:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
