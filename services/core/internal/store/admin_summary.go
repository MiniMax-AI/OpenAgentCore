package store

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type AdminSummaryFilter struct{ CreatedAfter, CreatedBefore *time.Time }
type AdminAssetCounts struct {
	Agents               int64 `json:"agents"`
	Skills               int64 `json:"skills"`
	EnvironmentTemplates int64 `json:"environment_templates"`
	Files                int64 `json:"files"`
	Vaults               int64 `json:"vaults"`
	Credentials          int64 `json:"credentials"`
}

// ReadAdminSummary visits one space's Sessions from one read-only snapshot. The
// visitor reuses the API's Session projection instead of creating another status
// or usage model. Paging keeps the stored configurations out of an unbounded slice.
func (s *Store) ReadAdminSummary(ctx context.Context, tenantID string, filter AdminSummaryFilter, visit func(sessions.Session, *string) error) (AdminAssetCounts, error) {
	var counts AdminAssetCounts
	tenant, err := parseID(tenantID)
	if err != nil {
		return counts, err
	}
	if visit == nil || filter.CreatedAfter != nil && filter.CreatedBefore != nil && !filter.CreatedAfter.Before(*filter.CreatedBefore) {
		return counts, sessions.ErrInvalidInput
	}
	err = s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		raw, err := q.AdminAssetCounts(ctx, tenant)
		if err != nil {
			return err
		}
		counts = AdminAssetCounts{Agents: raw.Agents, Skills: raw.Skills, EnvironmentTemplates: raw.EnvironmentTemplates, Files: raw.Files, Vaults: raw.Vaults, Credentials: raw.Credentials}
		params := sqlc.AdminSummarySessionsParams{TenantID: tenant, CreatedAfter: summaryTimestamp(filter.CreatedAfter), CreatedBefore: summaryTimestamp(filter.CreatedBefore), AfterID: pgtype.UUID{Valid: true}}
		for {
			rows, err := q.AdminSummarySessions(ctx, params)
			if err != nil {
				return err
			}
			for _, row := range rows {
				session, err := sessionpg.SessionFromRow(row.Session)
				if err != nil {
					return err
				}
				session, err = sessionpg.LoadSessionActivity(ctx, q, session)
				if err != nil {
					return err
				}
				var creator *string
				if row.CreationKeyID.Valid {
					id := row.CreationKeyID.String
					creator = &id
				}
				if err := visit(session, creator); err != nil {
					return err
				}
				params.AfterID = row.Session.ID
			}
			if len(rows) < 100 {
				return nil
			}
		}
	})
	return counts, err
}

func summaryTimestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}
