package store

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/agentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testAgents builds the Agent adapter and service on the database and
// credential key that built the test's Store, for tests that need saved Agents
// as fixtures.
func testAgents(t testing.TB, pool *pgxpool.Pool, cipher *credentialcrypto.Cipher) (*agentpg.Store, *agents.Service) {
	t.Helper()
	agentStore := agentpg.New(pgunit.NewPool(pool), cipher)
	agentService, err := agents.NewService(agentStore)
	if err != nil {
		t.Fatal(err)
	}
	return agentStore, agentService
}
