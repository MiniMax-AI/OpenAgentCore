package node

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func probe(context.Context) (*sandbox.CheckpointCompatibility, error) { return nil, nil }

// testDeployment is the deployment Core tells a node built from identity().
func testDeployment() *sandbox.NodeDeployment {
	id := identity()
	return &sandbox.NodeDeployment{Generation: id.DeploymentGeneration, SpecificationDigest: id.SpecificationDigest}
}

// newTestHub completes the generation callbacks a test does not observe:
// every reported generation is kept and the deployment targets the identity.
func newTestHub(options HubOptions) *Hub {
	if options.Generations == nil {
		options.Generations = func(context.Context, Identity, string, uint64, Health) error { return nil }
	}
	if options.Retention == nil {
		options.Retention = func(_ context.Context, id Identity, _ string, _ uint64, refs []sandbox.GenerationReference) (sandbox.NodeDeployment, []sandbox.GenerationRetention, error) {
			grants := make([]sandbox.GenerationRetention, 0, len(refs))
			for _, ref := range refs {
				grants = append(grants, sandbox.GenerationRetention{GenerationReference: ref, Keep: true})
			}
			return *testDeployment(), grants, nil
		}
	}
	return NewHub(options)
}

// runProvider serves provider as the identity's only generation until ctx ends.
func runProvider(ctx context.Context, config AgentConfig, provider sandbox.SandboxProvider, probe func(context.Context) (*sandbox.CheckpointCompatibility, error)) error {
	manager, err := NewGenerationManager(ctx, GenerationManagerOptions{
		Initial: []GenerationProvider{{Generation: config.Identity.DeploymentGeneration, SpecificationDigest: config.Identity.SpecificationDigest, Provider: provider, Probe: probe}},
		Prepare: func(context.Context, uint64, string) (GenerationProvider, error) {
			return GenerationProvider{}, errors.New("unexpected preparation")
		},
		Remove: func(context.Context, GenerationProvider) error { return nil },
	})
	if err != nil {
		return err
	}
	defer manager.Close()
	// Settle the first probe now so the first connection reports its outcome.
	manager.mu.Lock()
	g := manager.values[config.Identity.DeploymentGeneration]
	g.state = "ready"
	if checkpoint, err := probe(ctx); err != nil {
		g.state, g.diagnostic = "failed", sandbox.NodeDiagnostic(err)
	} else {
		g.checkpoint = checkpoint
	}
	manager.mu.Unlock()
	config.Generations = manager
	return Run(ctx, config)
}
