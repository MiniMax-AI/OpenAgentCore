package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// sessionReads is the Session adapter on pool without a credential key. It
// serves the Session reads that open no frozen data.
func sessionReads(pool *pgxpool.Pool) *sessionpg.Store {
	return sessionpg.New(pgunit.NewPool(pool), nil)
}

// fixtureSessions builds the Session adapter and service on db, with the
// placement rules on db's public URL, as cmd/server does.
func fixtureSessions(db fixtureDB) (*sessionpg.Store, *sessions.Service, error) {
	rules, err := placement.NewRules(providers.Builtin(), db.publicURL)
	if err != nil {
		return nil, nil, err
	}
	sessionStore := fixtureSessionStore(db)
	sessionService, err := sessions.NewService(sessionStore, rules)
	return sessionStore, sessionService, err
}

// fixtureSessionStore is the Session adapter on db with its credential key, as
// cmd/server builds it.
func fixtureSessionStore(db fixtureDB) *sessionpg.Store {
	return sessionpg.New(pgunit.NewPool(db.pool), db.cipher)
}

// fixtureSessionService is the Session service of fixtureSessions(db).
func fixtureSessionService(t testing.TB, db fixtureDB) *sessions.Service {
	t.Helper()
	_, service, err := fixtureSessions(db)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
