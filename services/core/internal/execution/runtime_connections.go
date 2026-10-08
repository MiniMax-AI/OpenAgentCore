package execution

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// environmentConnections holds the connection generation of each Environment
// with a live Link resource, and the generation of each live Link resource
// this process saw. The Worker's pass and the hosted lifecycle's quiesce
// publish through it; mu orders a whole pass before or after a quiesce's
// publish, so no pass republishes an Environment that quiesced.
type environmentConnections struct {
	mu      sync.Mutex
	current map[string]*runtimeConnection
	// served maps each live Link resource, without its generation, to the
	// generation last seen. The relay is process-local, so a restart starts
	// both empty.
	served map[sandboxbootstrap.Resource]uint64
}

// Durable generations fence old observations; a runtimeConnection only
// remembers this process's generation of an Environment's connection.
type runtimeConnection struct {
	tenant     string
	generation string
	revision   int64
	connected  bool
}

// observeSandboxConnections revokes at the relay each Link resource whose
// Serve authority ended, then publishes each Environment with a live Link
// resource as connected while the relay holds its serve peer, and as
// disconnected once its resource is gone. It leaves a quiesced Environment to
// the hosted lifecycle, which publishes it disconnected once its Runtime has
// quiesced, until the wake that resumes its compute.
func (w *Worker) observeSandboxConnections(ctx context.Context) error {
	c := w.connections
	c.mu.Lock()
	defer c.mu.Unlock()
	resources, err := w.dispatcher.SessionsReader.ListLiveSandboxResources(ctx)
	if err != nil {
		return err
	}
	w.revokeEndedResources(resources)
	live := make(map[string]bool, len(resources))
	for _, resource := range resources {
		live[resource.Resource.EnvironmentID] = true
		if resource.Quiesced {
			continue
		}
		serving := w.dispatcher.Links.Serving(resource.Resource.Ref())
		if err := observeRuntimeConnection(ctx, w.dispatcher.sessionExecution, c.current, resource.Resource.TenantID, resource.Resource.EnvironmentID, serving); err != nil && !environmentGone(err) {
			return err
		}
	}
	for environment, current := range c.current {
		if live[environment] {
			continue
		}
		if err := observeRuntimeConnection(ctx, w.dispatcher.sessionExecution, c.current, current.tenant, environment, false); err != nil && !environmentGone(err) {
			return err
		}
		delete(c.current, environment)
	}
	return nil
}

// revokeEndedResources revokes at the relay the previous generation of each
// resource whose generation advanced, and the last seen generation of each
// resource that is no longer live, which covers credential revocation and
// rotation, Environment expiry and Session deletion. It revokes only on those
// transitions, because each revocation advances the relay's epoch. The caller
// holds w.connections.mu.
func (w *Worker) revokeEndedResources(resources []sessions.SandboxResource) {
	served := w.connections.served
	live := make(map[sandboxbootstrap.Resource]bool, len(resources))
	for _, resource := range resources {
		key := resource.Resource
		key.Generation = 0
		live[key] = true
		if previous, seen := served[key]; seen && previous < resource.Resource.Generation {
			w.dispatcher.Links.RevokeResource(withGeneration(key, previous).Ref())
		}
		served[key] = resource.Resource.Generation
	}
	for key, generation := range served {
		if !live[key] {
			w.dispatcher.Links.RevokeResource(withGeneration(key, generation).Ref())
			delete(served, key)
		}
	}
}

func withGeneration(resource sandboxbootstrap.Resource, generation uint64) sandboxbootstrap.Resource {
	resource.Generation = generation
	return resource
}

// environmentGone reports a connection observation of an Environment that is
// gone, failed or expired.
func environmentGone(err error) bool {
	return errors.Is(err, sessions.ErrNotFound) || errors.Is(err, sessions.ErrInvalidInput)
}

// Each Environment has one observer: the Worker's pass over Link resources,
// with the hosted lifecycle's quiesce. The first connection this process
// observes starts a durable generation; each change advances its revision.
func observeRuntimeConnection(ctx context.Context, operations *sessions.ExecutionOperations, connections map[string]*runtimeConnection, tenant, environment string, connected bool) error {
	current := connections[environment]
	if connected && current == nil {
		generation := uuid.NewString()
		if err := operations.ReplaceEnvironmentConnection(ctx, tenant, environment, generation); err != nil {
			return err
		}
		current = &runtimeConnection{tenant: tenant, generation: generation}
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
