package sessionpg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var _ sessions.SessionReader = (*Store)(nil)

func (s *Store) GetSession(ctx context.Context, tenantID, sessionID string) (sessions.Session, error) {
	session, _, err := s.SessionStreamSnapshot(ctx, tenantID, sessionID)
	return session, err
}

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
	var page sessions.Page
	err = s.units.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if cursor != "" {
			after, err := q.GetSession(ctx, sqlc.GetSessionParams{TenantID: tenant, ID: pgunit.PathID(cursor)})
			if err != nil {
				return err
			}
			params.AfterCreated, params.AfterID = after.CreatedAt, after.ID
		}
		rows, err := q.ListSessions(ctx, params)
		if err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}
		page.Sessions = make([]sessions.Session, 0, min(limit, len(rows)))
		if len(rows) > limit {
			page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
			rows = rows[:limit]
		}
		for _, row := range rows {
			session, err := sessionFromRow(row)
			if err == nil {
				session, err = loadSessionActivity(ctx, q, session)
			}
			if err != nil {
				return err
			}
			page.Sessions = append(page.Sessions, session)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Page{}, sessions.ErrNotFound
	}
	return page, err
}

func (s *Store) SessionStreamSnapshot(ctx context.Context, tenantID, sessionID string) (sessions.Session, int64, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.Session{}, 0, err
	}
	var session sessions.Session
	var cursor int64
	err = s.units.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		session, cursor, err = loadSession(ctx, sqlc.New(tx), tenant, pgunit.PathID(sessionID))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Session{}, 0, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Session{}, 0, fmt.Errorf("get session: %w", err)
	}
	return session, cursor, nil
}

func (s *Store) SessionEventCursor(ctx context.Context, tenantID, sessionID string) (int64, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return 0, err
	}
	cursor, err := s.units.Queries().SessionEventCursor(ctx, sqlc.SessionEventCursorParams{TenantID: tenant, ID: pgunit.PathID(sessionID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, sessions.ErrNotFound
	}
	return cursor, err
}

func (s *Store) ListSessionEvents(ctx context.Context, tenantID, sessionID string, after int64) ([]sessions.SessionChange, error) {
	if after < 0 {
		return nil, sessions.ErrInvalidInput
	}
	latest, err := s.SessionEventCursor(ctx, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return nil, err
	}
	rows, err := s.units.Queries().ListSessionEvents(ctx, sqlc.ListSessionEventsParams{TenantID: tenant, SessionID: id, Sequence: after})
	if err != nil {
		return nil, err
	}
	changes := make([]sessions.SessionChange, 0, len(rows))
	if len(rows) == 0 && latest > after {
		return nil, sessions.ErrStreamGap
	}
	for _, row := range rows {
		if row.Sequence != after+1 {
			return nil, sessions.ErrStreamGap
		}
		var change sessions.SessionChange
		decoder := json.NewDecoder(bytes.NewReader(row.Payload))
		decoder.UseNumber()
		if err := decoder.Decode(&change); err != nil {
			return nil, err
		}
		change.Sequence = row.Sequence
		changes = append(changes, change)
		after = row.Sequence
	}
	return changes, nil
}

func (s *Store) GetTurnDiagnosticsSnapshot(ctx context.Context, tenantID, sessionID, turnID string) (sessions.TurnDiagnosticsSnapshot, error) {
	params, err := publicTurnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return sessions.TurnDiagnosticsSnapshot{}, err
	}
	result := sessions.TurnDiagnosticsSnapshot{Items: []sessions.ItemDiagnosticTiming{}}
	err = s.units.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		turn, err := q.GetTurn(ctx, params)
		if err != nil {
			return err
		}
		row, err := q.GetSession(ctx, sqlc.GetSessionParams{TenantID: params.TenantID, ID: params.SessionID})
		if err != nil {
			return err
		}
		result.Session, err = sessionFromRow(row)
		if err != nil {
			return err
		}
		result.Turn = TurnFromRow(turn)
		rows, err := q.ListTurnItemDiagnostics(ctx, sqlc.ListTurnItemDiagnosticsParams{SessionID: params.SessionID, TurnID: params.ID})
		if err != nil {
			return err
		}
		result.ItemsTruncated = len(rows) > 1000
		if result.ItemsTruncated {
			rows = rows[:1000]
		}
		for _, row := range rows {
			item := sessions.ItemDiagnosticTiming{ItemID: uuid.UUID(row.ID.Bytes).String(), StartedAt: row.CreatedAt.Time}
			if row.SettledAt.Valid {
				at := row.SettledAt.Time
				item.CompletedAt = &at
			}
			result.Items = append(result.Items, item)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.TurnDiagnosticsSnapshot{}, sessions.ErrNotFound
	}
	return result, err
}

func (s *Store) GetSessionExecutionConfiguration(ctx context.Context, tenantID, sessionID string) (v1.SessionExecutionConfiguration, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return v1.SessionExecutionConfiguration{}, err
	}
	row, err := s.units.Queries().GetSessionExecutionConfiguration(ctx, sqlc.GetSessionExecutionConfigurationParams{TenantID: tenant, SessionID: pgunit.PathID(sessionID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return v1.SessionExecutionConfiguration{}, sessions.ErrNotFound
	}
	if err != nil {
		return v1.SessionExecutionConfiguration{}, fmt.Errorf("get session execution configuration: %w", err)
	}
	var projection v1.SessionExecutionConfiguration
	if len(row.ExecutionConfiguration) == 0 {
		model, err := sessions.ExecutionModel(row.SessionConfiguration)
		if err != nil {
			return projection, err
		}
		var harness *string
		if row.Engine != "" {
			harness = &row.Engine
		}
		projection.Model = v1.ExecutionSelection{Value: model, Source: "unknown"}
		projection.Harness = v1.ExecutionSelection{Value: harness, Source: "unknown"}
		projection.ModelProvider = v1.ExecutionProviderSelection{Source: "unknown", Status: "unavailable"}
	} else if err := json.Unmarshal(row.ExecutionConfiguration, &projection); err != nil {
		return v1.SessionExecutionConfiguration{}, errors.New("invalid stored session execution configuration")
	}
	sessions.NormalizeExecutionProjection(&projection, uuid.UUID(row.ID.Bytes).String())
	return projection, nil
}

func (s *Store) MeasuredSessionUsage(ctx context.Context, tenantID, sessionID string) (json.RawMessage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return nil, err
	}
	usage, err := s.units.Queries().SessionMeasuredTokenUsage(ctx, sqlc.SessionMeasuredTokenUsageParams{TenantID: tenant, ID: id})
	if err != nil {
		return nil, fmt.Errorf("read measured session usage: %w", err)
	}
	return usage, nil
}

func (s *Store) GetManagedSessionArchive(ctx context.Context, tenantID, sessionID string) (sessions.ManagedArchive, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.ManagedArchive{}, err
	}
	return loadManagedArchive(ctx, s.units.Queries(), tenant, pgunit.PathID(sessionID))
}

// loadManagedArchive reads the resource disposal of the tenant's visible
// hosted Session.
func loadManagedArchive(ctx context.Context, q *sqlc.Queries, tenant, session pgtype.UUID) (sessions.ManagedArchive, error) {
	row, err := q.GetManagedSessionArchive(ctx, sqlc.GetManagedSessionArchiveParams{TenantID: tenant, ID: session})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ManagedArchive{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.ManagedArchive{}, err
	}
	if row.EnvironmentType != "openai_hosted" {
		return sessions.ManagedArchive{}, sessions.ErrInvalidInput
	}
	return sessions.ManagedArchive{SessionID: uuid.UUID(row.SessionID.Bytes).String(), EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(), State: row.State}, nil
}

// loadSession reads, on q, the tenant's visible Session with the projection
// of GetSession and its event cursor; a missing one is pgx.ErrNoRows.
func loadSession(ctx context.Context, q *sqlc.Queries, tenant, id pgtype.UUID) (sessions.Session, int64, error) {
	row, err := q.GetSession(ctx, sqlc.GetSessionParams{TenantID: tenant, ID: id})
	if err != nil {
		return sessions.Session{}, 0, err
	}
	session, err := sessionFromRow(row)
	if err != nil {
		return sessions.Session{}, 0, err
	}
	session, err = loadSessionActivity(ctx, q, session)
	return session, row.EventSequence, err
}

// loadSessionActivity adds the Environment, input activity and latest Turn
// projection of GetSession to session within the caller's snapshot.
func loadSessionActivity(ctx context.Context, q *sqlc.Queries, session sessions.Session) (sessions.Session, error) {
	id, _ := parseID(session.ID)
	tenant, _ := parseID(session.TenantID)
	environment, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenant, ID: id})
	if err == nil {
		value, err := environmentFromRow(environment.Environment, environment.TenantID, environment.Configuration, nil)
		if err != nil {
			return session, err
		}
		session.Environment = &value
		session.EnvironmentFailure = environmentFailure(environment.Environment)
		state, err := LoadEnvironmentInput(ctx, q, id)
		if err != nil {
			return session, err
		}
		session.EnvironmentInputActivity, session.PendingInput = sessions.InputActivity(state)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return session, err
	}
	row, err := q.GetLatestSessionTurn(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, nil
	}
	if err != nil {
		return session, err
	}
	turn := TurnFromRow(row)
	session.LastTurn = &turn
	session.RequiredActions, err = LoadRequiredActions(ctx, q, id, turn)
	if err != nil {
		return session, err
	}
	session.Usage, err = q.SessionTokenUsage(ctx, id)
	return session, err
}

// environmentFailure returns the recorded provisioning failure of a failed
// Environment, with its detail sanitized.
func environmentFailure(row sqlc.Environment) *sessions.EnvironmentFailure {
	if row.Status != "failed" || !row.FailureReason.Valid || !row.FailedAt.Valid {
		return nil
	}
	failure := &sessions.EnvironmentFailure{Reason: row.FailureReason.String, FailedAt: row.FailedAt.Time}
	var detail sessions.ProvisioningFailureDetail
	if json.Unmarshal(row.FailureDetail, &detail) == nil {
		failure.Detail = sessions.SanitizedProvisioningDetail(detail)
	}
	return failure
}
