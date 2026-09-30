package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/google/uuid"
)

type managedNodes struct {
	setup         *managedSetup
	runtime       *execution.RuntimeProvider
	hub           *node.Hub
	admin         *api.DeploymentAuthenticator
	closeProvider func()
}

// configureManagedNodes serves the nodes of the Web-managed deployment. Node
// presence and health and the generation of each allocation go through the
// deployment service; the owner epoch that fences connections and the
// allocations each generation retains are read from the deployment reader.
func configureManagedNodes(nodes *deployment.Service, reader deployment.Reader, registry *providers.Registry, publicURL string, owner func(context.Context) error) (*managedNodes, error) {
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
			return nodes.HeartbeatGenerations(ctx, n.NodeID, connection, epoch, nodeHealthRecord(health), health.Generations)
		},
		Retention: func(ctx context.Context, n node.Identity, connection string, epoch uint64, refs []sandbox.GenerationReference) (sandbox.NodeDeployment, []sandbox.GenerationRetention, error) {
			if err := owner(ctx); err != nil {
				return sandbox.NodeDeployment{}, nil, err
			}
			// The node waits for this answer; bound it like every leased operation.
			ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
			defer cancel()
			return nodes.NodeRetention(ctx, n.NodeID, connection, epoch, refs)
		},
		Authenticate: func(ctx context.Context, id, credential string) (node.Identity, error) {
			n, err := nodes.AuthenticateNode(ctx, id, credential)
			if errors.Is(err, deployment.ErrNodeCredential) {
				err = node.ErrAuthentication
			}
			return node.Identity{SpecificationDigest: n.SpecificationDigest, DeploymentGeneration: n.DeploymentGeneration, NodeID: n.NodeID, InstallationID: n.InstallationID, Provider: n.Provider, BackendFingerprint: n.BackendFingerprint, MaxActive: n.MaxActive, MaxRetained: n.MaxRetained}, err
		},
		OwnerEpoch: func(ctx context.Context) (uint64, error) {
			if err := owner(ctx); err != nil {
				return 0, err
			}
			return reader.OwnerEpoch(ctx)
		},
		Connected: func(ctx context.Context, n node.Identity, connection string, epoch uint64) error {
			if err := owner(ctx); err != nil {
				return err
			}
			return nodes.ConnectNode(ctx, n.NodeID, connection, epoch)
		},
		Disconnected: func(ctx context.Context, n node.Identity, connection string, epoch uint64) {
			_ = nodes.DisconnectNode(ctx, n.NodeID, connection, epoch)
		},
		Heartbeat: func(ctx context.Context, n node.Identity, connection string, epoch uint64, health node.Health) error {
			if err := owner(ctx); err != nil {
				return err
			}
			return nodes.Heartbeat(ctx, n.NodeID, connection, epoch, nodeHealthRecord(health))
		},
	})
	result.setup = &managedSetup{processPaths: providerProcessPaths(), registry: registry, deployment: nodes, allocations: reader, hub: result.hub, installationID: setupID, publicURL: publicURL}
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

func nodeHealthRecord(health node.Health) deployment.NodeHealth {
	return deployment.NodeHealth{Host: &deployment.NodeHost{EffectiveCPUCores: health.EffectiveCPUCores, CPUUtilization: health.CPUUtilization, TotalMemoryBytes: health.TotalMemoryBytes, AvailableMemoryBytes: health.AvailableMemoryBytes, AvailableDiskBytes: health.AvailableDiskBytes, ObservedAt: &health.ObservedAt}, ProviderReady: health.ProviderReady, Diagnostic: health.Diagnostic, CPUCount: health.CPUCount, AvailableMemoryBytes: health.AvailableMemoryBytes, AvailableDiskBytes: health.AvailableDiskBytes}
}
