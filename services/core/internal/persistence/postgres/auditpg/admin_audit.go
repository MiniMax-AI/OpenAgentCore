package auditpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
)

// ListAdminAudit pages committed administrator mutations across the
// deployment, newest first, with the ID as the tie breaker.
func (s *Store) ListAdminAudit(ctx context.Context, filter adminaudit.Filter) (adminaudit.Page, error) {
	page := adminaudit.Page{Data: []adminaudit.Operation{}}
	filter, err := filter.Validate()
	if err != nil {
		return page, err
	}
	query := filter
	query.After, query.Limit = "", 0
	query.CreatedAfter, query.CreatedBefore = utc(query.CreatedAfter), utc(query.CreatedBefore)
	scope := cursorScope(query)
	params := sqlc.ListAdminAuditLogParams{ProjectID: filter.ProjectID, ResourceType: filter.ResourceType, ResourceID: filter.ResourceID, Action: filter.Action, CreatedAfter: timestamp(filter.CreatedAfter), CreatedBefore: timestamp(filter.CreatedBefore), AfterID: pgtype.UUID{Valid: true}, PageLimit: int32(filter.Limit + 1)}
	var rows []sqlc.AdminAuditLog
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if filter.After != "" {
			id, ok := decodeCursor(filter.After, scope)
			if !ok {
				return adminaudit.ErrInvalidQuery
			}
			var err error
			params.AfterID = id
			params.AfterTime, err = q.AdminAuditCursor(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return adminaudit.ErrInvalidQuery
			}
			if err != nil {
				return err
			}
		}
		var err error
		rows, err = q.ListAdminAuditLog(ctx, params)
		return err
	})
	if err != nil {
		return page, err
	}
	page.HasMore = len(rows) > filter.Limit
	if page.HasMore {
		rows = rows[:filter.Limit]
	}
	for _, row := range rows {
		page.Data = append(page.Data, adminaudit.Operation{ID: uuid.UUID(row.ID.Bytes).String(), CreatedAt: row.CreatedAt.Time, AdminCredentialID: row.AdminCredentialID, ActorLabel: row.ActorLabel, Action: row.Action, ProjectID: projectID(row.ProjectID), ResourceType: row.ResourceType, ResourceID: row.ResourceID, ResultIDs: row.ResultIds, RequestID: row.RequestID, TraceID: row.TraceID})
	}
	if page.HasMore {
		page.NextCursor = encodeCursor(page.Data[len(page.Data)-1].ID, scope)
	}
	return page, nil
}

func projectID(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	value := uuid.UUID(id.Bytes).String()
	return &value
}
