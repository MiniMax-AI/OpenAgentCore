package sessionpg

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// parseID parses an internal identifier; a malformed one is
// sessions.ErrInvalidInput.
func parseID(value string) (pgtype.UUID, error) {
	id, err := pgunit.ParseID(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
	}
	return id, nil
}

// TurnLookup parses the internal identifiers of a tenant's Turn; a malformed
// one is sessions.ErrInvalidInput.
func TurnLookup(tenantID, sessionID, turnID string) (sqlc.GetTurnParams, error) {
	var p sqlc.GetTurnParams
	var err error
	if p.TenantID, err = parseID(tenantID); err != nil {
		return p, err
	}
	if p.SessionID, err = parseID(sessionID); err != nil {
		return p, err
	}
	p.ID, err = parseID(turnID)
	return p, err
}

// DeviceLookup parses a tenant and the internal identifier of one of its
// resources, such as a device, Session or Environment; a malformed one is
// sessions.ErrInvalidInput.
func DeviceLookup(tenantID, id string) (sqlc.GetDeviceParams, error) {
	var p sqlc.GetDeviceParams
	var err error
	if p.TenantID, err = parseID(tenantID); err != nil {
		return p, err
	}
	p.ID, err = parseID(id)
	return p, err
}

// TurnFromRow maps a stored Turn.
func TurnFromRow(row sqlc.Turn) sessions.Turn {
	return sessions.Turn{
		ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), Status: row.Status,
		CreatedAt: row.CreatedAt.Time, StartedAt: row.StartedAt.Time, CompletedAt: row.CompletedAt.Time,
		CancelRequestedAt: row.CancelRequestedAt.Time, Outcome: json.RawMessage(row.Outcome), Usage: json.RawMessage(row.TokenUsage),
		ArtifactCaptureStarted: row.ArtifactCaptureStarted,
	}
}

// SessionFromRow maps a stored Session with its creator, normalized
// configuration and metadata.
func SessionFromRow(row sqlc.Session) (sessions.Session, error) {
	session := sessions.Session{ID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String(), Engine: row.Engine, CreatedAt: row.CreatedAt.Time, RequiredActions: []v1.FunctionCallAction{}}
	creator, err := sessionCreator(row.CreatorKind, row.CreatorID)
	if err != nil {
		return sessions.Session{}, err
	}
	session.Creator = creator
	configuration, err := jsonobject.Normalize(row.Configuration)
	if err != nil {
		return sessions.Session{}, fmt.Errorf("decode session configuration: %w: %w", sessions.ErrInvalidInput, err)
	}
	session.Configuration = configuration
	if err := json.Unmarshal(row.Metadata, &session.Metadata); err != nil {
		return sessions.Session{}, fmt.Errorf("decode session metadata: %w", err)
	}
	return session, nil
}

// sessionCreator maps the stored creator: nil when none is recorded, and an
// error for a partial or invalid one.
func sessionCreator(kind, id pgtype.Text) (*identity.Subject, error) {
	if !kind.Valid && !id.Valid {
		return nil, nil
	}
	creator := identity.Subject{Kind: kind.String, ID: id.String}
	if !kind.Valid || !id.Valid || creator.Validate() != nil {
		return nil, fmt.Errorf("invalid stored Session creator")
	}
	return &creator, nil
}

// EnvironmentFromRow maps a stored Environment of the tenant with its
// normalized configuration snapshot, from the result of the query that read
// it: no rows is sessions.ErrNotFound.
func EnvironmentFromRow(row sqlc.Environment, tenant pgtype.UUID, configuration []byte, err error) (sessions.Environment, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Environment{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Environment{}, fmt.Errorf("get environment: %w", err)
	}
	configuration, err = jsonobject.Normalize(configuration)
	if err != nil {
		return sessions.Environment{}, fmt.Errorf("decode environment configuration: %w: %w", sessions.ErrInvalidInput, err)
	}
	return sessions.Environment{
		ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(),
		TenantID: uuid.UUID(tenant.Bytes).String(), Status: row.Status,
		Initialization: row.Initialization, CreatedAt: row.CreatedAt.Time, Configuration: configuration,
	}, nil
}
