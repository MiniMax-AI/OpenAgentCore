package dispatch_test

import (
	"context"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// preparedFixture is a disposable fault-injection resource; Start transfers it
// to one Session. A cancellablePreparation follows that Session across Start.
type preparedFixture interface {
	Start(context.Context, string, proto.MessageInput, chan<- proto.Envelope) (agent.Session, error)
	Close() error
}

type cancellablePreparation interface {
	preparedFixture
	agent.Session
}

type preparationFactory func(context.Context, proto.PromptRequestPayload) (preparedFixture, error)

// Old fault-injection fixtures model disposable executors, not reusable native implementations.
func preparationExecutorFixture(factory preparationFactory) agent.ExecutorFactory {
	return func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
		prepared, err := factory(ctx, req)
		if prepared == nil {
			return nil, err
		}
		return &preparationExecutor{prepared: prepared}, err
	}
}

type preparationExecutor struct {
	prepared  preparedFixture
	mu        sync.Mutex
	cancelled bool
}

func (e *preparationExecutor) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancelled {
		return nil
	}
	var err error
	if resource, ok := e.prepared.(*retryablePreparation); ok {
		err = resource.Close()
	} else if cancel, ok := e.prepared.(cancellablePreparation); ok {
		err = cancel.Cancel(ctx)
	} else {
		err = e.prepared.Close()
	}
	e.cancelled = err == nil
	return err
}

func (e *preparationExecutor) StartTurn(ctx context.Context, id string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	upstream := make(chan proto.Envelope, 64)
	terminal := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(out)
		defer once.Do(func() { close(terminal) })
		for event := range upstream {
			if event.Type == proto.TypeDone {
				once.Do(func() { close(terminal) })
			}
			out <- event
		}
	}()
	session, err := e.prepared.Start(ctx, id, input, upstream)
	if session == nil {
		close(upstream)
		// This fixture's old Start may have produced output before rejecting; retain its Turn.
		return &preparationTurn{owner: e, settled: terminal}, err
	}
	return &preparationTurn{Session: session, owner: e, settled: terminal}, err
}

type preparationTurn struct {
	agent.Session
	owner   *preparationExecutor
	settled <-chan struct{}
}

func (t *preparationTurn) Cancel(ctx context.Context) error { return t.owner.Close(ctx) }
func (t *preparationTurn) CancellationOutcome() proto.DonePayload {
	if target, ok := t.owner.prepared.(cancellablePreparation); ok {
		return target.CancellationOutcome()
	}
	return proto.DonePayload{}
}
func (t *preparationTurn) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	select {
	case <-t.settled:
		return agent.TurnSettlement{Reason: "disposable fault fixture"}, t.owner.Close(ctx)
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}
func (t *preparationTurn) SubmitFunctionResult(ctx context.Context, p proto.FunctionResultPayload) error {
	if target, ok := t.Session.(agent.FunctionResultSubmitter); ok {
		return target.SubmitFunctionResult(ctx, p)
	}
	return agent.ErrUnknownFunctionCall
}
func (t *preparationTurn) Steer(ctx context.Context, p proto.PromptSteerPayload) error {
	if target, ok := t.Session.(agent.Steerer); ok {
		return target.Steer(ctx, p)
	}
	return agent.ErrSteeringInactive
}
func (t *preparationTurn) SteerWithReceipt(ctx context.Context, p proto.PromptSteerPayload, write func()) error {
	if target, ok := t.Session.(agent.DurableSteerer); ok {
		return target.SteerWithReceipt(ctx, p, write)
	}
	if target, ok := t.Session.(agent.Steerer); ok {
		return target.Steer(ctx, p)
	}
	return agent.ErrSteeringInactive
}
