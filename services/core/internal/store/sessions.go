// Package store persists execution state independently of the Parsar product.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type Store struct {
	queries *sqlc.Queries
	// pool is the database the Store was built on; pooled runs every
	// transaction that is not on the execution lease.
	pool   *pgxpool.Pool
	pooled *pgunit.Pool
	// writer runs Session and execution-only transactions, and lease grants
	// execution authority. New sets writer to pooled and leaves lease nil;
	// NewExecution sets both to the lease it borrows. Neither changes later.
	writer           pgunit.Transactor
	lease            *pgunit.Lease
	credentialCipher *credentialcrypto.Cipher
	// publicURL is OAC_PUBLIC_URL. Core derives every address it gives
	// nodes, sandboxes and administrators from it.
	publicURL string
}

func New(pool *pgxpool.Pool) *Store {
	pooled := pgunit.NewPool(pool)
	return &Store{queries: sqlc.New(pool), pool: pool, pooled: pooled, writer: pooled}
}

// CreateSession uses a project-scoped key to make retries safe, including
// concurrent submissions. Different input or creator with the same key conflicts.
func (s *Store) CreateSession(ctx context.Context, tenantID string, input sessions.CreateSession) (sessions.Session, error) {
	result, err := s.createSession(ctx, tenantID, input)
	return s.sessionActivity(ctx, result.Session, err)
}

func (s *Store) createSession(ctx context.Context, tenantID string, input sessions.CreateSession) (sessions.Creation, error) {
	if err := input.Creator.Validate(); err != nil {
		return sessions.Creation{}, fmt.Errorf("%w: %v", sessions.ErrInvalidInput, err)
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.Creation{}, err
	}
	input.Engine = strings.TrimSpace(input.Engine)
	if !sessions.ValidEngine(input.Engine) || strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 128 {
		return sessions.Creation{}, fmt.Errorf("%w: engine and idempotency key are required", sessions.ErrInvalidInput)
	}
	if input.Metadata == nil {
		input.Metadata = map[string]string{}
	}
	encodedMetadata, err := metadata.Encode(input.Metadata)
	if err != nil {
		return sessions.Creation{}, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
	}
	if len(input.Configuration) > 512*1024 {
		return sessions.Creation{}, fmt.Errorf("%w: configuration exceeds 512 KiB", sessions.ErrInvalidInput)
	}
	configuration, err := jsonobject.Normalize(input.Configuration)
	if err != nil {
		return sessions.Creation{}, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
	}
	var batch []sessions.Input
	var encodedInput json.RawMessage
	if len(input.InitialInputs) > 0 {
		batch, encodedInput, err = validateInitialInputs(input.InitialInputs)
		if err != nil {
			return sessions.Creation{}, err
		}
	}
	// The retry identity covers the configuration as requested; the marker added
	// for a deployment default below is not caller input.
	requested := configuration
	if input.ModelProvider != nil {
		if err := input.ModelProvider.ValidateHarness(input.Engine); err != nil {
			return sessions.Creation{}, fmt.Errorf("%w: %s", sessions.ErrInvalidInput, err)
		}
		var fields map[string]any
		if json.Unmarshal(configuration, &fields) != nil {
			return sessions.Creation{}, sessions.ErrInvalidInput
		}
		environment, _ := fields["environment"].(map[string]any)
		environmentType, _ := environment["type"].(string)
		if !v1.ModelProviderAllowed(environmentType, input.ModelProviderSource) {
			return sessions.Creation{}, fmt.Errorf("%w: this model provider source is not supported for the Session environment", sessions.ErrInvalidInput)
		}
		fields["model_provider_configured"] = true
		configuration, err = json.Marshal(fields)
		if err != nil {
			return sessions.Creation{}, err
		}
	}
	var initialization *environmentconfig.Setup
	if !input.Initialization.Empty() {
		initialization = &input.Initialization
	}
	// The retry identity carries a caller's provider key only as a keyed
	// fingerprint. A deployment default is not caller input: leaving it and its
	// configuration marker out keeps retries equivalent when the default is set,
	// replaced or removed.
	var fingerprinted *v1.ModelProviderInput
	hashed := configuration
	if input.ModelProviderSource == v1.ModelProviderSourceDeployment {
		hashed = requested
	} else if fingerprinted, err = s.fingerprintedProvider(input.ModelProvider); err != nil {
		return sessions.Creation{}, err
	}
	// JSON map keys are sorted by encoding/json, so key order does not affect retries.
	canonical, err := json.Marshal(struct {
		ModelProvider  *v1.ModelProviderInput `json:",omitempty"`
		Engine         string
		Metadata       map[string]string
		Configuration  json.RawMessage                 `json:",omitempty"`
		InitialInputs  json.RawMessage                 `json:",omitempty"`
		InitialFiles   []environmentconfig.InitialFile `json:",omitempty"`
		Initialization *environmentconfig.Setup        `json:",omitempty"`
	}{fingerprinted, input.Engine, input.Metadata, hashed, encodedInput, input.InitialFiles, initialization})
	if err != nil {
		return sessions.Creation{}, fmt.Errorf("%w: input: %v", sessions.ErrInvalidInput, err)
	}
	creationHash, err := s.creationRequestHash(input.CreationRequest)
	if err != nil {
		return sessions.Creation{}, err
	}
	hash := sha256.Sum256(canonical)
	params := sqlc.CreateSessionParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant, Engine: input.Engine,
		Metadata: encodedMetadata, IdempotencyKey: input.IdempotencyKey, RequestHash: hex.EncodeToString(hash[:]),
		Configuration: configuration, CreationRequestHash: creationHash,
		CreatorKind: pgtype.Text{String: input.Creator.Kind, Valid: true}, CreatorID: pgtype.Text{String: input.Creator.ID, Valid: true},
	}
	row, environment, err := s.createSessionResources(ctx, tenantID, params, batch, encodedInput, input.InitialFiles, input.Initialization, input.ModelProvider, input.ExecutionConfiguration, input.ModelProviderSource, input.DeploymentProviderRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Creation{}, sessions.ErrIdempotencyConflict
	}
	if err != nil {
		return sessions.Creation{}, fmt.Errorf("create session: %w", err)
	}
	session, err := sessionpg.SessionFromRow(row)
	session.Environment = environment
	return sessions.Creation{Session: session, Created: row.ID == params.ID, Cursor: row.EventSequence}, err
}

// GetSession always scopes lookup to the authenticated caller's tenant.
func (s *Store) GetSession(ctx context.Context, tenantID, sessionID string) (sessions.Session, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.Session{}, err
	}
	id := pgunit.PathID(sessionID)
	row, err := s.queries.GetSession(ctx, sqlc.GetSessionParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Session{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Session{}, fmt.Errorf("get session: %w", err)
	}
	session, decodeErr := sessionpg.SessionFromRow(row)
	return s.sessionActivity(ctx, session, decodeErr)
}

// ListSessions orders by creation time and ID. The cursor is the last returned
// session ID and must belong to the same tenant; it grants no additional access.
func (s *Store) ListSessions(ctx context.Context, tenantID, cursor string, limit int, ascending bool, agentID *string) (sessions.Page, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.Page{}, err
	}
	if limit < 1 || limit > 100 {
		return sessions.Page{}, fmt.Errorf("%w: page size must be 1..100", sessions.ErrInvalidInput)
	}
	params := sqlc.ListSessionsParams{TenantID: tenant, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if agentID != nil {
		params.AgentID = pgtype.Text{String: *agentID, Valid: true}
	}
	if cursor != "" {
		after, err := s.GetSession(ctx, tenantID, pgunit.LookupCursor(cursor))
		if err != nil {
			return sessions.Page{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListSessions(ctx, params)
	if err != nil {
		return sessions.Page{}, fmt.Errorf("list sessions: %w", err)
	}
	page := sessions.Page{Sessions: make([]sessions.Session, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		session, err := sessionpg.SessionFromRow(row)
		session, err = s.sessionActivity(ctx, session, err)
		if err != nil {
			return sessions.Page{}, err
		}
		page.Sessions = append(page.Sessions, session)
	}
	return page, nil
}

// parseID translates pgunit's identifier rule into store's invalid-input
// error. Path identifiers use pgunit.PathID and lookup cursors
// pgunit.LookupCursor.
func parseID(value string) (pgtype.UUID, error) {
	id, err := pgunit.ParseID(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
	}
	return id, nil
}
