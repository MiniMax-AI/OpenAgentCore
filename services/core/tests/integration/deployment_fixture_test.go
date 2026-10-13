package integration

import (
	"context"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

// deploymentStore builds the deployment store as cmd/server does, on s's
// database and credential key.
func deploymentStore(s *Store) *deploymentpg.Store {
	return deploymentpg.New(pgunit.NewPool(s.pool), s.credentialCipher)
}

// placementRules builds the placement rules as cmd/server does, on the
// built-in providers and publicURL.
func placementRules(t testing.TB, publicURL string) *placement.Rules {
	t.Helper()
	rules, err := placement.NewRules(providers.Builtin(), publicURL)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// deploymentService is fixtureDeploymentService(s) for a test.
func deploymentService(t testing.TB, s *Store) *deployment.Service {
	t.Helper()
	service, err := fixtureDeploymentService(s)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// deploymentExecution builds the deployment execution operations on w's
// execution lease, as cmd/server does for the Worker.
func deploymentExecution(t testing.TB, w *Store) *deployment.ExecutionOperations {
	t.Helper()
	if w.lease == nil {
		t.Fatal("deployment execution operations need an execution writer")
	}
	_, operations, err := fixtureDeploymentExecution(w, w.lease)
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

func fixtureNodeHeartbeat(ctx context.Context, store *Store, service *deployment.Service, nodeID, connection string, epoch uint64, health deployment.NodeHealth) error {
	var generation uint64
	var digest, provider string
	if err := store.pool.QueryRow(ctx, "SELECT n.deployment_generation,n.specification_digest,d.provider_kind FROM runtime_nodes n CROSS JOIN runtime_deployment d WHERE n.id=$1", nodeID).Scan(&generation, &digest, &provider); err != nil {
		return err
	}
	var statuses []sandbox.GenerationStatus
	if health.ProviderReady && provider == "microsandbox" {
		statuses = []sandbox.GenerationStatus{{Generation: generation, SpecificationDigest: digest, State: "ready", Checkpoint: &sandbox.CheckpointCompatibility{ArtifactDomain: "fixture-store", ExecutionClass: "fixture-runtime"}}}
	}
	return service.Heartbeat(ctx, nodeID, connection, epoch, health, statuses)
}
