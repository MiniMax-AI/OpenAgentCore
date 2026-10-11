package integration

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

// fixtureDeploymentService builds the deployment service as cmd/server does, on
// s's database, credential key and placement rules.
func fixtureDeploymentService(s *Store) (*deployment.Service, error) {
	adapter := deploymentStore(s)
	return deployment.NewService(adapter, adapter, providers.Builtin(), s.placement)
}

// fixtureOwnerEpoch reads the execution owner epoch from the deployment store,
// as cmd/server does to fence node connections.
func fixtureOwnerEpoch(t testing.TB, s *Store) uint64 {
	t.Helper()
	epoch, err := deploymentStore(s).OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return epoch
}

// fixtureDeploymentExecution builds the pooled deployment service and the
// deployment execution operations on lease, as cmd/server does for the Worker.
func fixtureDeploymentExecution(s *Store, lease *pgunit.Lease) (*deployment.Service, *deployment.ExecutionOperations, error) {
	service, err := fixtureDeploymentService(s)
	if err != nil {
		return nil, nil, err
	}
	changes, err := deployment.NewExecutionOperations(service, deploymentpg.NewExecution(lease, s.credentialCipher), engine.Catalog{})
	return service, changes, err
}
