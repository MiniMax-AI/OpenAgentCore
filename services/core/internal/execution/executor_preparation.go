package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// prepareTurnExecutor reserves one admission on the Runtime-owned Executor.
// Core does not cache native ownership. A replacement requires the Runtime to
// confirm cleanup and recover the exact Session history before returning ready.
func (d *Dispatcher) prepareTurnExecutor(ctx context.Context, peer *gateway.Session, tenant, session, turn string, request proto.PromptRequestPayload, expectedStatus string) (*preparedStart, error) {
	prepared, err := newPreparedStart(peer)
	if err != nil {
		return nil, err
	}
	request.RunID, request.ConversationID, request.Input = "", "", nil
	if err = send(ctx, peer, proto.TypeExecutionPrepare, prepared.requestID, proto.ExecutionPreparePayload{SessionID: session, Configuration: request}); err == nil {
		err = d.awaitTurnExecutor(ctx, tenant, session, turn, expectedStatus, prepared)
	}
	if err != nil {
		prepared.close()
		return nil, err
	}
	return prepared, nil
}

func (d *Dispatcher) awaitTurnExecutor(ctx context.Context, tenant, session, turn, expectedStatus string, prepared *preparedStart) error {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			current, err := d.Store.GetTurn(ctx, tenant, session, turn)
			if err != nil {
				return err
			}
			if current.Status != expectedStatus || !current.CancelRequestedAt.IsZero() {
				return store.ErrTurnConflict
			}
		case env, ok := <-prepared.sub.Events:
			if !ok {
				return errors.New("executor preparation control stream closed")
			}
			status, err := prepared.observation(env)
			if err != nil {
				return err
			}
			if status.State == "ready" && status.RunID == "" && status.ExecutorID != "" {
				return nil
			}
			if status.State != "preparing" {
				return errors.New("unexpected executor state before Turn start")
			}
		}
	}
}
