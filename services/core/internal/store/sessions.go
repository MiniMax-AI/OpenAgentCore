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
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
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
	// placement admits and places hosted Session creation. It is nil until
	// SetPlacement, and hosted creation fails without it.
	placement *placement.Rules
}

// SetPlacement records the installation's placement rules, built once in the
// composition root. Call it once, before serving requests.
func (s *Store) SetPlacement(rules *placement.Rules) { s.placement = rules }

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

// sessionActivity adds the activity projection to a Session read outside a
// snapshot, in a snapshot of its own.
func (s *Store) sessionActivity(ctx context.Context, session sessions.Session, err error) (sessions.Session, error) {
	if err != nil {
		return sessions.Session{}, err
	}
	err = s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		session, err = sessionpg.LoadSessionActivity(ctx, s.queries.WithTx(tx), session)
		return err
	})
	return session, err
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
