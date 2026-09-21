package execution

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// authorizedRuntimePeer fences a connected socket against current credential
// authority. A stable device ID does not keep an old credential alive on rotation.
func authorizedRuntimePeer(ctx context.Context, s *store.Store, registry *gateway.Registry, id string) (*gateway.Session, error) {
	peer, err := registry.LookupDevice(id)
	if err != nil {
		return nil, err
	}
	credential, found, err := s.GetDeviceCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found || !peer.AuthenticatedWith(credential.CredentialHash) {
		peer.Close("Runtime authorization changed")
		return nil, store.ErrNotFound
	}
	if peer.IsClosed() {
		return nil, gateway.ErrSessionClosed
	}
	return peer, nil
}

func (d *Dispatcher) authorizedPeer(ctx context.Context, id string) (*gateway.Session, error) {
	return authorizedRuntimePeer(ctx, d.Store, d.Registry, id)
}
