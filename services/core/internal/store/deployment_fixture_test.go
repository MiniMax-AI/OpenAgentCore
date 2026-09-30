package store

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

// deploymentStore builds the deployment store as cmd/server does, on s's
// database and credential key.
func deploymentStore(s *Store) *deploymentpg.Store {
	return deploymentpg.New(pgunit.NewPool(s.pool), s.credentialCipher)
}

// deploymentService builds the deployment service as cmd/server does, on s's
// database and credential key, with s's current public URL.
func deploymentService(t testing.TB, s *Store) *deployment.Service {
	t.Helper()
	adapter := deploymentStore(s)
	service, err := deployment.NewService(adapter, adapter, providers.Builtin(), s.publicURL)
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
	operations, err := deployment.NewExecutionOperations(deploymentService(t, w), deploymentpg.NewExecution(w.lease, w.credentialCipher))
	if err != nil {
		t.Fatal(err)
	}
	return operations
}
