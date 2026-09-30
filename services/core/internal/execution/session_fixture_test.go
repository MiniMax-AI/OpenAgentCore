package execution

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// testSessions builds the pooled Session adapter on pool with cipher and the
// Session service on it, as cmd/server does.
func testSessions(t *testing.T, pool *pgxpool.Pool, cipher *credentialcrypto.Cipher) (*sessionpg.Store, *sessions.Service) {
	t.Helper()
	adapter := sessionpg.New(pgunit.NewPool(pool), cipher)
	service, err := sessions.NewService(adapter)
	if err != nil {
		t.Fatal(err)
	}
	return adapter, service
}
