package store_test

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// sessionReads is the Session adapter on pool. It serves the Item, Subagent
// and Artifact reads.
func sessionReads(pool *pgxpool.Pool) *sessionpg.Store { return sessionpg.New(pgunit.NewPool(pool)) }

// fixtureSessions builds the Session adapter and service on db, as cmd/server does.
func fixtureSessions(db fixtureDB) (*sessionpg.Store, *sessions.Service, error) {
	sessionStore := sessionReads(db.pool)
	sessionService, err := sessions.NewService(sessionStore)
	return sessionStore, sessionService, err
}
