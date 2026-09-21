package execution

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Access is serialized by the existing lifecycle gate. Durable generations fence
// old observations; this map only remembers the currently observed socket.
type runtimeConnection struct {
	peer       *gateway.Session
	generation string
	revision   int64
	connected  bool
}

func (r *runtimeLifecycle) observeConnection(ctx context.Context, owner store.RuntimeAllocation) error {
	if owner.Initialization != "complete" {
		return nil
	}
	bound, err := r.store.GetSessionDevice(ctx, owner.TenantID, owner.SessionID)
	if err != nil {
		return err
	}
	if bound.ID != owner.DeviceID || bound.EnvironmentID != owner.EnvironmentID {
		return store.ErrDeviceBindingConflict
	}
	if !owner.CreateSettled || owner.State != "running" {
		return nil
	}
	peer, err := authorizedRuntimePeer(ctx, r.store, r.registry, owner.DeviceID)
	connected := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, gateway.ErrSessionClosed) && !errors.Is(err, gateway.ErrDeviceNotRegistered) {
		return err
	}
	return observeRuntimeConnection(ctx, r.store, r.connections, owner.TenantID, owner.EnvironmentID, peer, connected)
}

// Each Environment has one observer: the hosted lifecycle or the Worker loop for
// enrolled user compute. Both publish the same durable generation/revision rules.
func observeRuntimeConnection(ctx context.Context, s *store.Store, connections map[string]*runtimeConnection, tenant, environment string, peer *gateway.Session, connected bool) error {
	current := connections[environment]
	if connected && (current == nil || current.peer != peer) {
		generation := uuid.NewString()
		if err := s.ReplaceEnvironmentConnection(ctx, tenant, environment, generation); err != nil {
			return err
		}
		current = &runtimeConnection{peer: peer, generation: generation}
		connections[environment] = current
	}
	if current == nil || current.connected == connected {
		return nil
	}
	current.revision++
	if err := s.ObserveEnvironmentConnection(ctx, tenant, environment, current.generation, current.revision, connected); err != nil {
		return err
	}
	current.connected = connected
	return nil
}

func (w *Worker) observeEnrolledRuntimes(ctx context.Context) error {
	bindings, err := w.dispatcher.Store.ListEnrolledRuntimeBindings(ctx)
	if err != nil {
		return err
	}
	live := make(map[string]bool, len(bindings))
	for _, bound := range bindings {
		live[bound.EnvironmentID] = true
		peer, err := w.dispatcher.authorizedPeer(ctx, bound.DeviceID)
		connected := err == nil
		if err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, gateway.ErrSessionClosed) && !errors.Is(err, gateway.ErrDeviceNotRegistered) {
			return err
		}
		if err := observeRuntimeConnection(ctx, w.dispatcher.Store, w.enrolledConnections, bound.TenantID, bound.EnvironmentID, peer, connected); err != nil {
			if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidInput) {
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
