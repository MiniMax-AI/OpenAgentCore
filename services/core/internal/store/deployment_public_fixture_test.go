package store_test

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

// fixtureDeployment builds the deployment service on db, as cmd/server does.
func fixtureDeployment(t testing.TB, db fixtureDB) *deployment.Service {
	t.Helper()
	service, err := fixtureDeploymentService(db)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func fixtureDeploymentService(db fixtureDB) (*deployment.Service, error) {
	rules, err := placement.NewRules(providers.Builtin(), db.publicURL)
	if err != nil {
		return nil, err
	}
	adapter := deploymentpg.New(pgunit.NewPool(db.pool), db.cipher)
	return deployment.NewService(adapter, adapter, providers.Builtin(), rules)
}

// fixtureRules builds the placement rules on db's public URL, as cmd/server
// does for the Store's Session creation.
func fixtureRules(t testing.TB, db fixtureDB) *placement.Rules {
	t.Helper()
	rules, err := placement.NewRules(providers.Builtin(), db.publicURL)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// fixtureReader is the deployment reader on db, as cmd/server builds it.
func fixtureReader(db fixtureDB) *deploymentpg.Store {
	return deploymentpg.New(pgunit.NewPool(db.pool), db.cipher)
}

// fixtureOwnerEpoch reads the execution owner epoch from the deployment store,
// as cmd/server does to fence node connections.
func fixtureOwnerEpoch(t testing.TB, db fixtureDB) uint64 {
	t.Helper()
	epoch, err := deploymentpg.New(pgunit.NewPool(db.pool), db.cipher).OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return epoch
}

// fixtureDeploymentExecution builds the pooled deployment service and the
// deployment execution operations on lease, as cmd/server does for the Worker.
func fixtureDeploymentExecution(db fixtureDB, lease *pgunit.Lease) (*deployment.Service, *deployment.ExecutionOperations, error) {
	service, err := fixtureDeploymentService(db)
	if err != nil {
		return nil, nil, err
	}
	changes, err := deployment.NewExecutionOperations(service, deploymentpg.NewExecution(lease, db.cipher))
	return service, changes, err
}
