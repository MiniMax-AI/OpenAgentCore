package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"

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
		r.forgetAwaitingConnection(owner.DeviceID)
		return nil
	}
	if err != nil {
		return err
	}
	if bound.ID != owner.DeviceID || bound.EnvironmentID != owner.EnvironmentID {
		return sessions.ErrDeviceBindingConflict
	}
	if !owner.CreateSettled || owner.State != "running" {
		r.forgetAwaitingConnection(owner.DeviceID)
		return nil
	}
	peer, err := authorizedRuntimePeer(ctx, r.sessions, r.registry, owner.DeviceID)
	connected := err == nil
	if err != nil && !errors.Is(err, sessions.ErrNotFound) && !errors.Is(err, runtimegateway.ErrSessionClosed) && !errors.Is(err, runtimegateway.ErrDeviceNotRegistered) {
		return err
	}
	if errors.Is(err, runtimegateway.ErrDeviceNotRegistered) && r.connectionWake != nil {
		if r.awaiting == nil {
			r.awaiting = make(map[string]deployment.AllocationKey)
		}
		r.awaiting[owner.DeviceID] = owner.Key()
		r.registry.WatchDevice(owner.DeviceID, r.connectionWake)
	} else {
		// A registered closed or unauthorized peer is not a missing registration.
		// Watching it would signal immediately and spin until it deregisters.
		r.forgetAwaitingConnection(owner.DeviceID)
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

func (r *runtimeLifecycle) forgetAwaitingConnection(device string) {
	if _, found := r.awaiting[device]; !found {
		return
	}
	delete(r.awaiting, device)
	r.registry.UnwatchDevice(device, r.connectionWake)
}

func (r *runtimeLifecycle) clearConnectionWatches() {
	for device := range r.awaiting {
		r.forgetAwaitingConnection(device)
	}
}

// Registration signals coalesce, so recheck every outstanding device under the
// existing lifecycle owner. This pass never calls the Sandbox Provider.
func (r *runtimeLifecycle) observeAwaitingConnections(parent context.Context) error {
	ctx, finish, err := r.beginReconcile(parent)
	if err != nil {
		return err
	}
	defer finish()
	for device, key := range r.awaiting {
		operation, stop := context.WithTimeout(ctx, 5*time.Second)
		owner, err := r.reader.EnvironmentAllocation(operation, key)
		switch {
		case err == nil && owner.DeviceID == device && owner.ProviderKey == r.config.InstallationID && owner.NodeID == r.nodeID && owner.State == "running" && owner.CreateSettled && !owner.SessionDeleted && !owner.Expired && (owner.ComputePhase == "disabled" || owner.ComputePhase == "running"):
			err = r.observeConnection(operation, owner)
		case err == nil || errors.Is(err, deployment.ErrNotFound):
			r.forgetAwaitingConnection(device)
			err = nil
		}
		stop()
		if err != nil {
			if ownership := r.lease.CheckOwnership(ctx); ownership != nil {
				return ownership
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			log.Ctx(ctx).Warn("managed Runtime connection observation incomplete", "environment_id", key.EnvironmentID)
		}
	}
	return nil
}
