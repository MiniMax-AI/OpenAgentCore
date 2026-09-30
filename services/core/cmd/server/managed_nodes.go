package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type managedNodes struct {
	setup         *managedSetup
	runtime       *execution.RuntimeProvider
	hub           *node.Hub
	admin         *api.DeploymentAuthenticator
	closeProvider func()
}

func configureManagedNodes(s *store.Store, publicURL string, owner func(context.Context) error) (*managedNodes, error) {
	setupID := os.Getenv("OAC_INSTALLATION_ID")
	if setupID == "" {
		return nil, nil
	}
	id, err := uuid.Parse(setupID)
	if err != nil || id == uuid.Nil || id.String() != setupID {
		return nil, errors.New("sandbox installation ID must be a canonical UUID")
	}
	if publicURL == "" {
		return nil, errors.New("OAC_INSTALLATION_ID requires OAC_PUBLIC_URL, the origin nodes and sandboxes use to reach Core")
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
	if result.admin == nil {
		return nil, errors.New("Web sandbox setup requires OAC_CORE_KEY_DIGESTS_FILE with the Core key digest")
	}
	result.hub = node.NewHub(node.HubOptions{
		Generations: func(ctx context.Context, n node.Identity, connection string, epoch uint64, health node.Health) error {
			if err := owner(ctx); err != nil {
				return err
			}
			return s.HeartbeatRuntimeNodeGenerations(ctx, n.NodeID, connection, epoch, nodeHealthRecord(health), health.Generations)
		},
		Retention: func(ctx context.Context, n node.Identity, connection string, epoch uint64, refs []sandbox.GenerationReference) (sandbox.NodeDeployment, []sandbox.GenerationRetention, error) {
			if err := owner(ctx); err != nil {
				return sandbox.NodeDeployment{}, nil, err
			}
			return s.RuntimeNodeRetention(ctx, n.NodeID, connection, epoch, refs)
		},
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
			return s.HeartbeatRuntimeNode(ctx, n.NodeID, connection, epoch, nodeHealthRecord(health))
		},
	})
	result.setup = &managedSetup{processPaths: providerProcessPaths(), store: s, hub: result.hub, installationID: setupID, publicURL: publicURL}
	result.runtime = execution.NewDeferredRuntimeProvider(setupID, result.setup.load, result.setup.prepare)
	result.runtime.PublishUnconfigured = result.setup.publishUnconfigured
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
	path := os.Getenv("OAC_CORE_KEY_DIGESTS_FILE")
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read OAC_CORE_KEY_DIGESTS_FILE")
	}
	var digests []string
	if json.Unmarshal(raw, &digests) != nil || len(digests) == 0 {
		return nil, errors.New("OAC_CORE_KEY_DIGESTS_FILE must contain a JSON array of Core key SHA-256 digests")
	}
	return api.NewDeploymentAuthenticator(digests)
}

func serverAddress() string {
	if value := os.Getenv("OAC_ADDR"); value != "" {
		return value
	}
	return "127.0.0.1:8091"
}

func nodeHealthRecord(health node.Health) store.RuntimeNodeHealth {
	return store.RuntimeNodeHealth{Host: &store.RuntimeNodeHost{EffectiveCPUCores: health.EffectiveCPUCores, CPUUtilization: health.CPUUtilization, TotalMemoryBytes: health.TotalMemoryBytes, AvailableMemoryBytes: health.AvailableMemoryBytes, AvailableDiskBytes: health.AvailableDiskBytes, ObservedAt: &health.ObservedAt}, ProviderReady: health.ProviderReady, Diagnostic: health.Diagnostic, CPUCount: health.CPUCount, AvailableMemoryBytes: health.AvailableMemoryBytes, AvailableDiskBytes: health.AvailableDiskBytes}
}
