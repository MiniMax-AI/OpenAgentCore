package sessionpg

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var (
	_ sessions.ArtifactReader  = (*Store)(nil)
	_ sessions.ArtifactStorage = (*Store)(nil)
)

func (s *Store) GetSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string) (sessions.Artifact, error) {
	lookup, err := artifactLookup(tenantID, sessionID, artifactID)
	if err != nil {
		return sessions.Artifact{}, err
	}
	row, err := loadArtifact(ctx, s.units.Queries(), lookup)
	if err != nil {
		return sessions.Artifact{}, err
	}
	return artifactFromRow(row), nil
}

func (s *Store) ListSessionArtifacts(ctx context.Context, tenantID, sessionID, environmentID, cursor string, limit int, ascending bool) (sessions.ArtifactPage, error) {
	if limit < 1 || limit > 100 {
		return sessions.ArtifactPage{}, sessions.ErrInvalidInput
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.ArtifactPage{}, err
	}
	session := pgunit.PathID(sessionID)
	q := s.units.Queries()
	if err := visibleSession(ctx, q, tenant, session); err != nil {
		return sessions.ArtifactPage{}, err
	}
	params := sqlc.ListSessionArtifactsParams{TenantID: tenant, SessionID: session, PageLimit: int32(limit + 1), Ascending: ascending, AfterID: pgtype.UUID{Valid: true}}
	if environmentID != "" {
		// A malformed filter matches nothing, like another Environment's ID (HE-56).
		params.EnvironmentID = pgunit.PathID(environmentID)
	}
	if cursor != "" {
		// Any cursor that is not an Artifact of this Session, including a
		// malformed one, is an invalid cursor rather than a missing resource.
		after, err := loadArtifact(ctx, q, sqlc.GetSessionArtifactParams{TenantID: tenant, SessionID: session, ID: pgunit.PathID(cursor)})
		if errors.Is(err, sessions.ErrNotFound) {
			// The Session lookup above is a separate statement: a Session deleted
			// since then stays not found. Deleted Sessions never reappear, so an
			// existing one here also existed when the cursor was read.
			if err := visibleSession(ctx, q, tenant, session); err != nil {
				return sessions.ArtifactPage{}, err
			}
			return sessions.ArtifactPage{}, sessions.ErrArtifactCursor
		}
		if err != nil {
			return sessions.ArtifactPage{}, err
		}
		params.AfterCreated = after.CreatedAt
		params.AfterID = after.ID
	}
	rows, err := q.ListSessionArtifacts(ctx, params)
	if err != nil {
		return sessions.ArtifactPage{}, err
	}
	page := sessions.ArtifactPage{Artifacts: make([]sessions.Artifact, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Artifacts = append(page.Artifacts, artifactFromRow(row))
	}
	return page, nil
}

// ReadSessionArtifact keeps an admitted snapshot available across concurrent
// deletion.
func (s *Store) ReadSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string, consume func(sessions.Artifact, io.Reader) error) error {
	lookup, err := artifactLookup(tenantID, sessionID, artifactID)
	if err != nil {
		return err
	}
	if consume == nil {
		return sessions.ErrInvalidInput
	}
	return s.units.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row, err := loadArtifact(ctx, sqlc.New(tx), lookup)
		if err != nil {
			return err
		}
		objects := tx.LargeObjects()
		body, err := objects.Open(ctx, row.BodyOid.Uint32, pgx.LargeObjectModeRead)
		if err != nil {
			return err
		}
		if err := consume(artifactFromRow(row), body); err != nil {
			return err
		}
		return body.Close()
	})
}

func (s *Store) DeleteSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string) error {
	lookup, err := artifactLookup(tenantID, sessionID, artifactID)
	if err != nil {
		return err
	}
	return s.units.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		// Use the same lock order as whole-Session deletion and Turn publication.
		locked, err := LockSession(ctx, q, lookup.TenantID, lookup.SessionID)
		if err != nil {
			return err
		}
		if err := locked.Public(); err != nil {
			return err
		}
		oid, err := q.DeleteSessionArtifact(ctx, sqlc.DeleteSessionArtifactParams(lookup))
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		objects := tx.LargeObjects()
		if err := objects.Unlink(ctx, oid.Uint32); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "delete", "artifact", uuid.UUID(lookup.ID.Bytes).String(), uuid.UUID(lookup.SessionID.Bytes).String())
	})
}

// WithArtifactStaging runs the staging in one pooled transaction, not on the
// execution lease: the transfer can outlast a lease transaction, and staged
// Artifacts stay private until the lease-bound completion publishes or
// discards them. Large objects the staging created are removed with the
// rolled-back transaction.
func (s *Store) WithArtifactStaging(ctx context.Context, key sessions.ArtifactStagingKey, stage func(context.Context, sessions.ArtifactStagingTx) error) error {
	turn, err := TurnLookup(key.TenantID, key.SessionID, key.TurnID)
	if err != nil {
		return err
	}
	environment, err := parseID(key.EnvironmentID)
	if err != nil {
		return err
	}
	return s.units.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return stage(ctx, &artifactStaging{tx: tx, q: sqlc.New(tx), turn: turn, environment: environment})
	})
}

// artifactStaging is one staging transaction. It keeps the content put so far
// until StageArtifacts records it.
type artifactStaging struct {
	tx          pgx.Tx
	q           *sqlc.Queries
	turn        sqlc.GetTurnParams
	environment pgtype.UUID
	staged      []sqlc.StageSessionArtifactParams
}

func (t *artifactStaging) LoadEnvironment(ctx context.Context) (sessions.Environment, error) {
	row, err := t.q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: t.turn.TenantID, ID: t.turn.SessionID})
	return EnvironmentFromRow(row.Environment, row.TenantID, row.Configuration, err)
}

func (t *artifactStaging) PutArtifactContent(ctx context.Context, path string, size int64, content io.Reader) error {
	writer, err := pgunit.CreateLargeObject(ctx, t.tx)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(writer, content, size); err != nil {
		return err
	}
	body, err := writer.Close()
	if err != nil {
		return err
	}
	t.staged = append(t.staged, sqlc.StageSessionArtifactParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Path: path, SizeBytes: body.Size, BodyOid: pgtype.Uint32{Uint32: body.OID, Valid: true}, Sha256: body.SHA256})
	return nil
}

func (t *artifactStaging) LockSession(ctx context.Context) (sessions.LockedSession, error) {
	return LockSession(ctx, t.q, t.turn.TenantID, t.turn.SessionID)
}

func (t *artifactStaging) LoadTurn(ctx context.Context) (sessions.Turn, error) {
	row, err := t.q.GetTurn(ctx, t.turn)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Turn{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Turn{}, err
	}
	return TurnFromRow(row), nil
}

func (t *artifactStaging) StageArtifacts(ctx context.Context) error {
	for _, row := range t.staged {
		row.SessionID, row.TurnID, row.EnvironmentID = t.turn.SessionID, t.turn.ID, t.environment
		if err := t.q.StageSessionArtifact(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// artifactLookup resolves a public Artifact path: a malformed tenant is
// sessions.ErrInvalidInput, and a malformed Session or Artifact ID resolves as
// missing.
func artifactLookup(tenantID, sessionID, artifactID string) (sqlc.GetSessionArtifactParams, error) {
	tenant, err := parseID(tenantID)
	return sqlc.GetSessionArtifactParams{TenantID: tenant, SessionID: pgunit.PathID(sessionID), ID: pgunit.PathID(artifactID)}, err
}

// loadArtifact loads a published Artifact of a visible Session, or
// sessions.ErrNotFound.
func loadArtifact(ctx context.Context, q *sqlc.Queries, lookup sqlc.GetSessionArtifactParams) (sqlc.SessionArtifact, error) {
	row, err := q.GetSessionArtifact(ctx, lookup)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, sessions.ErrNotFound
	}
	return row, err
}

// visibleSession reports whether the tenant's Session exists and is not
// publicly deleted, as sessions.ErrNotFound.
func visibleSession(ctx context.Context, q *sqlc.Queries, tenant, session pgtype.UUID) error {
	_, err := q.GetSession(ctx, sqlc.GetSessionParams{TenantID: tenant, ID: session})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	return nil
}

func artifactFromRow(row sqlc.SessionArtifact) sessions.Artifact {
	return sessions.Artifact{ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(),
		TurnID: uuid.UUID(row.TurnID.Bytes).String(), EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(),
		Path: row.Path, SizeBytes: row.SizeBytes, CreatedAt: row.CreatedAt.Time}
}
