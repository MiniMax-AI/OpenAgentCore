package integration

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Store is the integration tests' fixture: one database with its credential
// key and placement rules, from which the tests build the domain services as
// cmd/server does. Session transactions run on writer: the pool, or the
// lease of a NewExecution fixture.
type Store struct {
	queries          *sqlc.Queries
	pool             *pgxpool.Pool
	pooled           *pgunit.Pool
	writer           pgunit.Transactor
	lease            *pgunit.Lease
	credentialCipher *credentialcrypto.Cipher
	placement        *placement.Rules
}

// defaultPlacement is the placement rules cmd/server builds on the built-in
// providers and an HTTPS public URL.
var defaultPlacement, _ = placement.NewRules(providers.Builtin(), "https://core.example")

// New is the fixture on pool without a credential key, under defaultPlacement.
func New(pool *pgxpool.Pool) *Store {
	pooled := pgunit.NewPool(pool)
	return &Store{queries: sqlc.New(pool), pool: pool, pooled: pooled, writer: pooled, placement: defaultPlacement}
}

// NewWithCredentialCipher is New with a credential key, so Sessions can freeze
// sealed resources.
func NewWithCredentialCipher(pool *pgxpool.Pool, cipher *credentialcrypto.Cipher) *Store {
	s := New(pool)
	s.credentialCipher = cipher
	return s
}

// NewExecution is s with its Session transactions on lease.
func NewExecution(s *Store, lease *pgunit.Lease) *Store {
	writer := *s
	writer.writer, writer.lease = lease, lease
	return &writer
}

// SetPlacement gives s the placement rules its Sessions are created under.
func (s *Store) SetPlacement(rules *placement.Rules) { s.placement = rules }

// CreateSession creates a Session through the Session service on s.
func (s *Store) CreateSession(ctx context.Context, tenant string, input sessions.CreateSession) (sessions.Session, error) {
	creation, err := createSession(ctx, s, tenant, input)
	return creation.Session, err
}

// createSession runs Session creation as cmd/server does, on s's database,
// credential key and placement rules.
func createSession(ctx context.Context, s *Store, tenant string, input sessions.CreateSession) (sessions.Creation, error) {
	service, err := newSessionService(s)
	if err != nil {
		return sessions.Creation{}, err
	}
	return service.CreateSession(ctx, tenant, input)
}

// parseID translates pgunit's identifier rule into the Session
// invalid-input error.
func parseID(value string) (pgtype.UUID, error) {
	id, err := pgunit.ParseID(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
	}
	return id, nil
}

// withSession runs apply in sessionpg's Session transaction on s's writer.
func (s *Store) withSession(ctx context.Context, tenantID, sessionID string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withSessionState(ctx, tenantID, sessionID, false, apply)
}

func (s *Store) withPublicSession(ctx context.Context, tenantID, sessionID string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withSessionState(ctx, tenantID, sessionID, true, apply)
}

func (s *Store) withSessionState(ctx context.Context, tenantID, sessionID string, public bool, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	// Public paths resolve malformed IDs as missing.
	id := pgunit.PathID(sessionID)
	if !public {
		if id, err = parseID(sessionID); err != nil {
			return err
		}
	}
	return sessionpg.WithSession(ctx, s.writer, tenant, id, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		if public {
			if err := locked.Public(); err != nil {
				return err
			}
		}
		return apply(ctx, q, id)
	})
}
