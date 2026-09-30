package store

import (
	"context"
	"errors"
	"io"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) GetSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string) (sessions.Artifact, error) {
	lookup, err := artifactLookup(tenantID, sessionID, artifactID)
	if err != nil {
		return sessions.Artifact{}, err
	}
	row, err := s.queries.GetSessionArtifact(ctx, lookup)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Artifact{}, sessions.ErrNotFound
	}
	return artifactFromRow(row), err
}

func (s *Store) ListSessionArtifacts(ctx context.Context, tenantID, sessionID, environmentID, cursor string, limit int, ascending bool) (sessions.ArtifactPage, error) {
	if limit < 1 || limit > 100 {
		return sessions.ArtifactPage{}, sessions.ErrInvalidInput
	}
	if _, err := s.GetSession(ctx, tenantID, sessionID); err != nil {
		return sessions.ArtifactPage{}, err
	}
	tenant, _ := parseID(tenantID)
	session, _ := parseID(sessionID)
	params := sqlc.ListSessionArtifactsParams{TenantID: tenant, SessionID: session, PageLimit: int32(limit + 1), Ascending: ascending, AfterID: pgtype.UUID{Valid: true}}
	if environmentID != "" {
		// A malformed filter matches nothing, like another Environment's ID (HE-56).
		params.EnvironmentID = pgunit.PathID(environmentID)
	}
	if cursor != "" {
		// Any cursor that is not an Artifact of this Session, including a
		// malformed one, is an invalid cursor rather than a missing resource.
		after, err := s.GetSessionArtifact(ctx, tenantID, sessionID, cursor)
		if errors.Is(err, sessions.ErrNotFound) {
			// The Session lookup above is a separate statement: a Session deleted
			// since then stays not found. Deleted Sessions never reappear, so an
			// existing one here also existed when the cursor was read.
			if _, err := s.GetSession(ctx, tenantID, sessionID); err != nil {
				return sessions.ArtifactPage{}, err
			}
			return sessions.ArtifactPage{}, sessions.ErrArtifactCursor
		}
		if err != nil {
			return sessions.ArtifactPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListSessionArtifacts(ctx, params)
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

// ReadSessionArtifact keeps an admitted snapshot available across concurrent deletion.
func (s *Store) ReadSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string, consume func(sessions.Artifact, io.Reader) error) error {
	lookup, err := artifactLookup(tenantID, sessionID, artifactID)
	if err != nil {
		return err
	}
	if consume == nil {
		return sessions.ErrInvalidInput
	}
	return s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.queries.WithTx(tx).GetSessionArtifact(ctx, lookup)
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		}
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
	return s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		// Use the same lock order as whole-Session deletion and Turn publication.
		locked, err := sessionpg.LockSession(ctx, q, lookup.TenantID, lookup.SessionID)
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

func artifactLookup(tenantID, sessionID, artifactID string) (sqlc.GetSessionArtifactParams, error) {
	ids, err := publicTurnLookup(tenantID, sessionID, artifactID)
	return sqlc.GetSessionArtifactParams{TenantID: ids.TenantID, SessionID: ids.SessionID, ID: ids.ID}, err
}

func artifactFromRow(row sqlc.SessionArtifact) sessions.Artifact {
	return sessions.Artifact{ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(),
		TurnID: uuid.UUID(row.TurnID.Bytes).String(), EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(),
		Path: row.Path, SizeBytes: row.SizeBytes, CreatedAt: row.CreatedAt.Time}
}
