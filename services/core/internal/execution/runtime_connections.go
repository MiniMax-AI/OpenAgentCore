package execution

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Access is serialized by the existing lifecycle gate. Durable generations fence
// old observations; this map only remembers the currently observed socket.
type runtimeConnection struct {
	peer       *runtimegateway.Session
	generation string
	revision   int64
	connected  bool
}

func (r *runtimeLifecycle) observeConnection(ctx context.Context, owner deployment.Allocation) error {
	bound, err := r.sessions.GetSessionRuntimeDevice(ctx, owner.TenantID, owner.SessionID)
	if errors.Is(err, sessions.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if bound.ID != owner.DeviceID || bound.EnvironmentID != owner.EnvironmentID {
		return sessions.ErrDeviceBindingConflict
	}
	if !owner.CreateSettled || owner.State != "running" {
		return nil
	}
	peer, err := authorizedRuntimePeer(ctx, r.sessions, r.registry, owner.DeviceID)
	connected := err == nil
	if err != nil && !errors.Is(err, sessions.ErrNotFound) && !errors.Is(err, runtimegateway.ErrSessionClosed) && !errors.Is(err, runtimegateway.ErrDeviceNotRegistered) {
		return err
	}
	return observeRuntimeConnection(ctx, r.sessionExecution, r.connections, owner.TenantID, owner.EnvironmentID, peer, connected)
}

// Each Environment has one observer: the hosted lifecycle or the Worker loop for
// enrolled user compute. Both publish the same durable generation/revision rules.
func observeRuntimeConnection(ctx context.Context, operations *sessions.ExecutionOperations, connections map[string]*runtimeConnection, tenant, environment string, peer *runtimegateway.Session, connected bool) error {
	current := connections[environment]
	if connected && (current == nil || current.peer != peer) {
		generation := uuid.NewString()
		if err := operations.ReplaceEnvironmentConnection(ctx, tenant, environment, generation); err != nil {
			return err
		}
		current = &runtimeConnection{peer: peer, generation: generation}
		connections[environment] = current
	}
	if current == nil || current.connected == connected {
		return nil
	}
	current.revision++
	if err := operations.ObserveEnvironmentConnection(ctx, tenant, environment, current.generation, current.revision, connected); err != nil {
		return err
	}
	current.connected = connected
	return nil
}

func (w *Worker) observeEnrolledRuntimes(ctx context.Context) error {
	bindings, err := w.dispatcher.SessionsReader.ListEnrolledRuntimeBindings(ctx)
	if err != nil {
		return err
	}
	live := make(map[string]bool, len(bindings))
	for _, bound := range bindings {
		live[bound.EnvironmentID] = true
		peer, err := w.dispatcher.authorizedPeer(ctx, bound.DeviceID)
		connected := err == nil
		if err != nil && !errors.Is(err, sessions.ErrNotFound) && !errors.Is(err, runtimegateway.ErrSessionClosed) && !errors.Is(err, runtimegateway.ErrDeviceNotRegistered) {
			return err
		}
		if err := observeRuntimeConnection(ctx, w.dispatcher.sessionExecution, w.enrolledConnections, bound.TenantID, bound.EnvironmentID, peer, connected); err != nil {
			if errors.Is(err, sessions.ErrNotFound) || errors.Is(err, sessions.ErrInvalidInput) {
				continue
			}
			return err
		}
	}
	for id, current := range w.enrolledConnections {
		if !live[id] {
			if current.peer != nil {
				current.peer.Close("Environment is no longer available")
			}
			delete(w.enrolledConnections, id)
		}
	}
	return nil
}
