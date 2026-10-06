package sessionpg

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) ListAdminRuntimeTargets(ctx context.Context, tenantIDs []string, after string, limit int, ascending bool) (sessions.AdminRuntimeTargetPage, error) {
	page := sessions.AdminRuntimeTargetPage{Data: []sessions.AdminRuntimeTarget{}}
	if limit < 1 || limit > 100 {
		return page, sessions.ErrInvalidInput
	}
	tenants := make([]pgtype.UUID, 0, len(tenantIDs))
	for _, value := range tenantIDs {
		id, err := parseID(value)
		if err != nil {
			return page, err
		}
		tenants = append(tenants, id)
	}
	q := s.units.Queries()
	params := sqlc.AdminRuntimeTargetsParams{TenantIds: tenants, Ascending: ascending, AfterID: pgtype.UUID{Valid: true}, PageLimit: int32(limit + 1)}
	if after != "" {
		var err error
		params.AfterID, err = parseID(after)
		if err != nil {
			return page, sessions.ErrNotFound
		}
		params.AfterTime, err = q.AdminRuntimeCursor(ctx, sqlc.AdminRuntimeCursorParams{ID: params.AfterID, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return page, sessions.ErrNotFound
		}
		if err != nil {
			return page, err
		}
	}
	rows, err := q.AdminRuntimeTargets(ctx, params)
	if err != nil {
		return page, err
	}
	page.HasMore = len(rows) > limit
	if page.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Data = append(page.Data, sessions.AdminRuntimeTarget{SessionID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String()})
	}
	return page, nil
}

func (s *Store) ReadAdminSummary(ctx context.Context, tenantID string, filter sessions.AdminSummaryFilter, visit func(sessions.Session, *string) error) (sessions.AdminAssetCounts, error) {
	var counts sessions.AdminAssetCounts
	tenant, err := parseID(tenantID)
	if err != nil {
		return counts, err
	}
	err = s.units.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		raw, err := q.AdminAssetCounts(ctx, tenant)
		if err != nil {
			return err
		}
		counts = sessions.AdminAssetCounts(raw)
		params := sqlc.AdminSummarySessionsParams{TenantID: tenant, CreatedAfter: timestamp(filter.CreatedAfter), CreatedBefore: timestamp(filter.CreatedBefore), AfterID: pgtype.UUID{Valid: true}}
		for {
			rows, err := q.AdminSummarySessions(ctx, params)
			if err != nil {
				return err
			}
			for _, row := range rows {
				session, err := SessionFromRow(row.Session)
				if err != nil {
					return err
				}
				if session, err = LoadSessionActivity(ctx, q, session); err != nil {
					return err
				}
				var creator *string
				if row.CreationKeyID.Valid {
					creator = &row.CreationKeyID.String
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

func timestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}
