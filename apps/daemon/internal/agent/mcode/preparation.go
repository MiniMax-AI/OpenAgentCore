package mcode

import (
	"context"
	"fmt"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Prepared retains its single admission API over the same native Executor.
// Runtime's Session lifecycle uses Executor directly.
type prepared struct {
	mu              sync.Mutex
	executor        *executor
	session         *Session
	started, closed bool
}

func NewPreparationFactory(config WorkspaceConfig) agent.PreparationFactory {
	factory := NewExecutorFactory(&config)
	return func(ctx context.Context, req proto.PromptRequestPayload) (agent.Prepared, error) {
		value, err := factory(ctx, req)
		if err != nil {
			if value != nil {
				return &prepared{executor: value.(*executor)}, err
			}
			return nil, err
		}
		e := value.(*executor)
		return &prepared{executor: e, session: newTurnSession(ctx, req, e.opts, e.connection, nil)}, nil
	}
}

func (p *prepared) Start(ctx context.Context, runID string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.started {
		return nil, fmt.Errorf("mcode: preparation is no longer available")
	}
	turn, err := p.executor.StartTurn(ctx, runID, input, out)
	if turn != nil {
		p.started, p.session = true, turn.(*Session)
	}
	return turn, err
}

func (p *prepared) Close() error {
	p.mu.Lock()
	started := p.started
	if !started {
		p.closed = true
	}
	p.mu.Unlock()
	if started {
		return nil
	}
	return p.executor.Close(context.Background())
}

func (p *prepared) Cancel(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	current, started := p.session, p.started
	p.mu.Unlock()
	if started {
		if err := current.Cancel(ctx); err != nil {
			// The single-use Prepared owns disposal as well as cancellation.
			if closeErr := p.executor.Close(ctx); closeErr != nil {
				return closeErr
			}
			return nil
		}
	}
	return p.executor.Close(ctx)
}

func (p *prepared) CancellationOutcome() proto.DonePayload {
	p.mu.Lock()
	s := p.session
	p.mu.Unlock()
	if s == nil {
		return proto.DonePayload{}
	}
	return s.CancellationOutcome()
}
