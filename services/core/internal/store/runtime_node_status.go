package store

import "context"

// RuntimeNodeStatus reports only the authenticated node's Core-owned presence.
type RuntimeNodeStatus struct {
	RuntimeNodeIdentity
	Connected     bool `json:"connected"`
	ProviderReady bool `json:"provider_ready"`
}

func (s *Store) RuntimeNodeStatus(ctx context.Context, nodeID, credential string) (RuntimeNodeStatus, error) {
	identity, err := s.AuthenticateRuntimeNode(ctx, nodeID, credential)
	if err != nil {
		return RuntimeNodeStatus{}, err
	}
	nodes, err := s.ListRuntimeNodes(ctx)
	if err != nil {
		return RuntimeNodeStatus{}, err
	}
	for _, node := range nodes {
		if node.ID == identity.NodeID {
			return RuntimeNodeStatus{RuntimeNodeIdentity: identity, Connected: node.Online, ProviderReady: node.Online && node.ProviderReady}, nil
		}
	}
	return RuntimeNodeStatus{}, ErrRuntimeNodeCredential
}
