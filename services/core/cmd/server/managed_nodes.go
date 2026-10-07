package main

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/processconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

type managedNodes struct {
	setup   *managedSetup
	runtime *execution.RuntimeProvider
	hub     *node.Hub
}

// configureManagedNodes serves the nodes of the Web-managed deployment. Node
// presence and health and the generation of each allocation go through the
// deployment service; the owner epoch that fences connections and the
// allocations each generation retains are read from the deployment reader. It
// returns nil without an installation ID.
func configureManagedNodes(nodes *deployment.Service, reader deployment.Reader, registry *providers.Registry, config processconfig.Config, owner func(context.Context) error) *managedNodes {
	if config.InstallationID == "" {
		return nil
	}
	result := &managedNodes{}
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
	// Load requires OAC_PUBLIC_URL with an installation ID.
	result.setup = &managedSetup{processPaths: config.ProviderPaths, registry: registry, deployment: nodes, allocations: reader, hub: result.hub, installationID: config.InstallationID, runtimeAPI: config.PublicOrigin.RuntimeAPI()}
	result.runtime = execution.NewDeferredRuntimeProvider(config.InstallationID, result.setup.load, result.setup.prepare)
	result.runtime.PublishUnconfigured = result.setup.publishUnconfigured
	return result
}

func (m *managedNodes) close() {
	if m != nil {
		m.hub.Close()
	}
}

func nodeHealthRecord(health node.Health) deployment.NodeHealth {
	return deployment.NodeHealth{Host: &deployment.NodeHost{EffectiveCPUCores: health.EffectiveCPUCores, CPUUtilization: health.CPUUtilization, TotalMemoryBytes: health.TotalMemoryBytes, AvailableMemoryBytes: health.AvailableMemoryBytes, AvailableDiskBytes: health.AvailableDiskBytes, ObservedAt: &health.ObservedAt}, ProviderReady: health.ProviderReady, Diagnostic: health.Diagnostic, CPUCount: health.CPUCount, AvailableMemoryBytes: health.AvailableMemoryBytes, AvailableDiskBytes: health.AvailableDiskBytes}
}
