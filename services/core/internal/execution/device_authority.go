package execution

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// authorizedRuntimePeer fences a connected socket against current credential
// authority. A stable device ID does not keep an old credential alive on rotation.
func authorizedRuntimePeer(ctx context.Context, s *store.Store, registry *runtimegateway.Registry, id string) (*runtimegateway.Session, error) {
	peer, err := registry.LookupDevice(id)
	if err != nil {
		return nil, err
	}
	credential, found, err := s.GetDeviceCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found || !peer.AuthenticatedWith(credential.CredentialHash) {
		// Reject new work even while this exact old delivery drains its receipt.
		draining, drainErr := peer.DrainArchivedCancellation(ctx)
		if drainErr != nil || !draining {
			peer.Close("Runtime authorization changed")
		}
		return nil, store.ErrNotFound
	}
	if peer.IsClosed() {
		return nil, runtimegateway.ErrSessionClosed
	}
	return peer, nil
}

func (d *Dispatcher) authorizedPeer(ctx context.Context, id string) (*runtimegateway.Session, error) {
	return authorizedRuntimePeer(ctx, d.Store, d.Registry, id)
}
