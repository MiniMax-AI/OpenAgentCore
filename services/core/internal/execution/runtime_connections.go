package execution

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// environmentConnections holds the connection generation of each Environment
// with a live Link resource. The Worker's pass and the hosted lifecycle's
// quiesce publish through it; mu orders a whole pass before or after a
// quiesce's publish, so no pass republishes an Environment that quiesced.
type environmentConnections struct {
	mu      sync.Mutex
	current map[string]*runtimeConnection
}

// Durable generations fence old observations; a runtimeConnection only
// remembers this process's generation of an Environment's connection.
type runtimeConnection struct {
	peer       *runtimegateway.Session
	tenant     string
	generation string
	revision   int64
	connected  bool
}

// observeSandboxConnections publishes each Environment with a live Link
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
	live := make(map[string]bool, len(resources))
	for _, resource := range resources {
		live[resource.Resource.EnvironmentID] = true
		if resource.Quiesced {
			continue
		}
		serving := w.dispatcher.Links.Serving(resource.Resource.Ref())
		if err := observeRuntimeConnection(ctx, w.dispatcher.sessionExecution, c.current, resource.Resource.TenantID, resource.Resource.EnvironmentID, nil, serving); err != nil && !environmentGone(err) {
			return err
		}
	}
	for environment, current := range c.current {
		if live[environment] {
			continue
		}
		if err := observeRuntimeConnection(ctx, w.dispatcher.sessionExecution, c.current, current.tenant, environment, nil, false); err != nil && !environmentGone(err) {
			return err
		}
		delete(c.current, environment)
	}
	return nil
}

// environmentGone reports a connection observation of an Environment that is
// gone, failed or expired.
func environmentGone(err error) bool {
	return errors.Is(err, sessions.ErrNotFound) || errors.Is(err, sessions.ErrInvalidInput)
}

// Each Environment has one observer: the Worker's pass over Link resources,
// with the hosted lifecycle's quiesce, or the Worker loop for enrolled user
// compute. Both publish the same durable generation/revision rules.
func observeRuntimeConnection(ctx context.Context, operations *sessions.ExecutionOperations, connections map[string]*runtimeConnection, tenant, environment string, peer *runtimegateway.Session, connected bool) error {
	current := connections[environment]
	if connected && (current == nil || current.peer != peer) {
		generation := uuid.NewString()
		if err := operations.ReplaceEnvironmentConnection(ctx, tenant, environment, generation); err != nil {
			return err
		}
		current = &runtimeConnection{peer: peer, tenant: tenant, generation: generation}
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
		if err := observeRuntimeConnection(ctx, w.dispatcher.sessionExecution, w.enrolledConnections, bound.TenantID, bound.EnvironmentID, peer, connected); err != nil && !environmentGone(err) {
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
