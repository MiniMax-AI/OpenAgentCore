package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateRuntimeEnrollment(ctx context.Context) (RuntimeEnrollmentToken, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return RuntimeEnrollmentToken{}, err
	}
	result := RuntimeEnrollmentToken{Token: hex.EncodeToString(bytes[:])}
	err := s.runtimeManagerTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if d.Mode != "nodes" || d.Maintenance {
			return ErrSandboxDeploymentConflict
		}
		if err := q.CreateRuntimeEnrollment(ctx, sqlc.CreateRuntimeEnrollmentParams{TokenSha256: runtimeTokenDigest(result.Token), InstallationID: d.InstallationID}); err != nil {
			return err
		}
		row, err := q.GetRuntimeEnrollment(ctx, runtimeTokenDigest(result.Token))
		result.ID, result.ExpiresAt = runtimeUUID(row.ID), row.ExpiresAt.Time
		return err
	})
	return result, err
}

func (s *Store) GetRuntimeEnrollmentReceipt(ctx context.Context, id string) (RuntimeEnrollmentReceipt, error) {
	parsed, err := parseConnectionGeneration(id)
	if err != nil {
		return RuntimeEnrollmentReceipt{}, err
	}
	row, err := s.queries.GetRuntimeEnrollmentReceipt(ctx, parsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeEnrollmentReceipt{}, ErrNotFound
	}
	if err != nil {
		return RuntimeEnrollmentReceipt{}, err
	}
	result := RuntimeEnrollmentReceipt{ID: runtimeUUID(row.ID), Status: "waiting", ExpiresAt: row.ExpiresAt.Time}
	if row.NodeID.Valid {
		id := runtimeUUID(row.NodeID)
		result.NodeID = &id
		result.Status = "enrolled"
	} else if !row.ExpiresAt.Time.After(time.Now()) {
		result.Status = "expired"
	}
	return result, nil
}
