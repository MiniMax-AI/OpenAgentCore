package claudesdk

import (
	"context"
	"errors"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// prepared is a single admission for direct adapter callers. It uses the same
// Executor as pooled Runtime execution; workspace reads retain the owner.
type prepared struct {
	mu       sync.Mutex
	executor *executor
	session  *session
	binding  *preparedStart
	closed   bool
}

type preparedStart struct {
	turn agent.Turn
	done chan struct{}
}

func NewPreparationFactory(config Config) agent.PreparationFactory {
	factory := NewExecutorFactory(config)
	return func(ctx context.Context, req proto.PromptRequestPayload) (agent.Prepared, error) {
		if config.Workspace == nil {
			return nil, errors.New("claudesdk: workspace preparation requires a bound workspace")
		}
		resource, err := factory(ctx, req)
		if resource == nil {
			return nil, err
		}
		e := resource.(*executor)
		return &prepared{executor: e, session: e.base}, err
	}
}

func (p *prepared) Start(ctx context.Context, run string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Session, error) {
	p.mu.Lock()
	if p.closed || p.binding != nil {
		p.mu.Unlock()
		return nil, errors.New("claudesdk: admission is unavailable")
	}
	binding := &preparedStart{done: make(chan struct{})}
	p.binding = binding
	p.mu.Unlock()
	turn, err := p.executor.StartTurn(ctx, run, input, out)
	p.mu.Lock()
	binding.turn = turn
	if turn == nil {
		p.binding = nil
	}
	close(binding.done)
	p.mu.Unlock()
	if turn != nil {
		go func() { _, _ = turn.AwaitSettlement(context.Background()); _ = p.executor.Close(context.Background()) }()
	}
	return turn, err
}

func (p *prepared) Close() error {
	p.mu.Lock()
	if p.binding != nil {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	return p.executor.Close(context.Background())
}

func (p *prepared) Cancel(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	p.closed = true
	binding := p.binding
	p.mu.Unlock()
	if binding == nil {
		return p.executor.Close(ctx)
	}
	select {
	case <-binding.done:
	default:
		if err := p.executor.Close(ctx); err != nil {
			return err
		}
		select {
		case <-binding.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if binding.turn == nil {
		return p.executor.Close(ctx)
	}
	if err := binding.turn.Cancel(ctx); err != nil {
		return err
	}
	p.executor.retire()
	return nil
}

func (p *prepared) CancellationOutcome() proto.DonePayload {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.binding == nil || p.binding.turn == nil {
		return proto.DonePayload{}
	}
	return p.binding.turn.CancellationOutcome()
}
