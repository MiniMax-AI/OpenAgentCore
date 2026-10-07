package execution

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// authorizedRuntimePeer fences a connected socket against current credential
// authority. A stable device ID does not keep an old credential alive on rotation.
func authorizedRuntimePeer(ctx context.Context, devices sessions.DeviceReader, registry *runtimegateway.Registry, id string) (*runtimegateway.Session, error) {
	peer, err := registry.LookupDevice(id)
	if err != nil {
		return nil, err
	}
	credential, found, err := devices.GetDeviceCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found || !peer.AuthenticatedWith(credential.CredentialHash) {
		// Reject new work even while this exact old delivery drains its receipt.
		draining, drainErr := peer.DrainArchivedCancellation(ctx)
		if drainErr != nil || !draining {
			peer.Close("Runtime authorization changed")
		}
		return nil, sessions.ErrNotFound
	}
	if peer.IsClosed() {
		return nil, runtimegateway.ErrSessionClosed
	}
	return peer, nil
}

func (d *Dispatcher) authorizedPeer(ctx context.Context, id string) (*runtimegateway.Session, error) {
	return authorizedRuntimePeer(ctx, d.SessionsReader, d.Registry, id)
}

// assignedRuntimePeer returns the authorized peer of the Session's bound
// Runtime after the Runtime acknowledged the Session's assignment.
func assignedRuntimePeer(ctx context.Context, devices sessions.DeviceReader, registry *runtimegateway.Registry, bound sessions.ExecutionDevice) (*runtimegateway.Session, error) {
	peer, err := authorizedRuntimePeer(ctx, devices, registry, bound.ID)
	if err != nil {
		return nil, err
	}
	if err := peer.Bind(ctx, bound.Assignment, bound.EnvironmentID); err != nil {
		return nil, err
	}
	return peer, nil
}

func (d *Dispatcher) assignedPeer(ctx context.Context, bound sessions.ExecutionDevice) (*runtimegateway.Session, error) {
	return assignedRuntimePeer(ctx, d.SessionsReader, d.Registry, bound)
}
