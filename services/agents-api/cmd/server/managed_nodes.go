package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type managedNodes struct {
	setup         *managedSetup
	runtime       *execution.RuntimeProvider
	hub           *node.Hub
	admin         *api.DeploymentAuthenticator
	closeProvider func()
}

func configureManagedNodes(s *store.Store, owner func(context.Context) error) (*managedNodes, error) {
	if os.Getenv("AGENTS_API_MANAGED_RUNTIMES_FILE") != "" {
		return nil, errors.New("file-managed sandbox configuration is no longer supported; retain existing resources, drain them with the previous release, this release does not automatically adopt file-managed deployment records")
	}
	setupID := os.Getenv("AGENTS_API_SANDBOX_INSTALLATION_ID")
	if setupID == "" {
		return nil, nil
	}
	id, err := uuid.Parse(setupID)
	if err != nil || id == uuid.Nil || id.String() != setupID {
		return nil, errors.New("sandbox installation ID must be a canonical UUID")
	}
	if os.Getenv("AGENTS_API_DAEMON_WS_URL") == "" {
		return nil, errors.New("sandbox setup requires daemon transport")
	}
	closeProvider := func() {}
	result := &managedNodes{closeProvider: closeProvider}
	success := false
	defer func() {
		if !success {
			closeProvider()
		}
	}()
	result.admin, err = deploymentAdminAuthenticator()
	if err != nil {
		return nil, err
	}
	if setupID != "" && result.admin == nil {
		return nil, errors.New("Web sandbox setup requires deployment administrator credentials")
	}
	result.hub = node.NewHub(node.HubOptions{
		Authenticate: func(ctx context.Context, id, credential string) (node.Identity, error) {
			n, err := s.AuthenticateRuntimeNode(ctx, id, credential)
			if errors.Is(err, store.ErrRuntimeNodeCredential) {
				err = node.ErrAuthentication
			}
			return node.Identity{SpecificationDigest: n.SpecificationDigest, DeploymentGeneration: n.DeploymentGeneration, NodeID: n.NodeID, InstallationID: n.InstallationID, Provider: n.Provider, BackendFingerprint: n.BackendFingerprint, MaxActive: n.MaxActive, MaxRetained: n.MaxRetained}, err
		},
		OwnerEpoch: func(ctx context.Context) (uint64, error) {
			if err := owner(ctx); err != nil {
				return 0, err
			}
			return s.RuntimeOwnerEpoch(ctx)
		},
		Connected: func(ctx context.Context, n node.Identity, connection string, epoch uint64) error {
			if err := owner(ctx); err != nil {
				return err
			}
			return s.ConnectRuntimeNode(ctx, n.NodeID, connection, epoch)
		},
		Disconnected: func(ctx context.Context, n node.Identity, connection string, epoch uint64) {
			_ = s.DisconnectRuntimeNode(ctx, n.NodeID, connection, epoch)
		},
		Heartbeat: func(ctx context.Context, n node.Identity, connection string, epoch uint64, health node.Health) error {
			if err := owner(ctx); err != nil {
				return err
			}
			return s.HeartbeatRuntimeNode(ctx, n.NodeID, connection, epoch, store.RuntimeNodeHealth{ProviderReady: health.ProviderReady, Diagnostic: health.Diagnostic, CPUCount: health.CPUCount, AvailableMemoryBytes: health.AvailableMemoryBytes, AvailableDiskBytes: health.AvailableDiskBytes})
		},
	})
	result.setup = &managedSetup{store: s, hub: result.hub, installationID: setupID}
	result.runtime = execution.NewDeferredRuntimeProvider(setupID, result.setup.load, result.setup.prepare)
	success = true
	return result, nil
}

func (m *managedNodes) close() {
	if m != nil {
		m.hub.Close()
		m.closeProvider()
	}
}

func deploymentAdminAuthenticator() (*api.DeploymentAuthenticator, error) {
	path := os.Getenv("AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE")
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE")
	}
	var digests []string
	if json.Unmarshal(raw, &digests) != nil || len(digests) == 0 {
		return nil, errors.New("sandbox administrator configuration must contain an array of SHA-256 digests")
	}
	return api.NewDeploymentAuthenticator(digests)
}

func serverAddress() string {
	if value := os.Getenv("AGENTS_API_ADDR"); value != "" {
		return value
	}
	return "127.0.0.1:8091"
}
