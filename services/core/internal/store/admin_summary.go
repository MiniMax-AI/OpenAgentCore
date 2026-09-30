package store

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
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
func (s *Store) ReadAdminSummary(ctx context.Context, tenantID string, filter AdminSummaryFilter, visit func(Session, *string) error) (AdminAssetCounts, error) {
	var counts AdminAssetCounts
	tenant, err := parseID(tenantID)
	if err != nil {
		return counts, err
	}
	if visit == nil || filter.CreatedAfter != nil && filter.CreatedBefore != nil && !filter.CreatedAfter.Before(*filter.CreatedBefore) {
		return counts, ErrInvalidInput
	}
	err = s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		raw, err := q.AdminAssetCounts(ctx, tenant)
		if err != nil {
			return err
		}
		counts = AdminAssetCounts{Agents: raw.Agents, Skills: raw.Skills, EnvironmentTemplates: raw.EnvironmentTemplates, Files: raw.Files, Vaults: raw.Vaults, Credentials: raw.Credentials}
		params := sqlc.AdminSummarySessionsParams{TenantID: tenant, CreatedAfter: auditTimestamp(filter.CreatedAfter), CreatedBefore: auditTimestamp(filter.CreatedBefore), AfterID: pgtype.UUID{Valid: true}}
		for {
			rows, err := q.AdminSummarySessions(ctx, params)
			if err != nil {
				return err
			}
			for _, row := range rows {
				session, err := sessionFromRow(row.Session)
				if err != nil {
					return err
				}
				session, err = readSessionActivity(ctx, q, session)
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

type AdminRuntimeTarget struct{ SessionID, TenantID string }
type AdminRuntimeTargetPage struct {
	Data    []AdminRuntimeTarget
	HasMore bool
}

func (s *Store) ListAdminRuntimeTargets(ctx context.Context, tenantIDs []string, after string, limit int, ascending bool) (AdminRuntimeTargetPage, error) {
	page := AdminRuntimeTargetPage{Data: []AdminRuntimeTarget{}}
	if limit < 1 || limit > 100 {
		return page, ErrInvalidInput
	}
	tenants := make([]pgtype.UUID, 0, len(tenantIDs))
	for _, value := range tenantIDs {
		id, err := parseID(value)
		if err != nil {
			return page, err
		}
		tenants = append(tenants, id)
	}
	params := sqlc.AdminRuntimeTargetsParams{TenantIds: tenants, Ascending: ascending, AfterID: pgtype.UUID{Valid: true}, PageLimit: int32(limit + 1)}
	if after != "" {
		var err error
		params.AfterID, err = parseID(after)
		if err != nil {
			return page, ErrNotFound
		}
		params.AfterTime, err = s.queries.AdminRuntimeCursor(ctx, sqlc.AdminRuntimeCursorParams{ID: params.AfterID, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return page, ErrNotFound
		}
		if err != nil {
			return page, err
		}
	}
	rows, err := s.queries.AdminRuntimeTargets(ctx, params)
	if err != nil {
		return page, err
	}
	page.HasMore = len(rows) > limit
	if page.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Data = append(page.Data, AdminRuntimeTarget{SessionID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String()})
	}
	return page, nil
}
