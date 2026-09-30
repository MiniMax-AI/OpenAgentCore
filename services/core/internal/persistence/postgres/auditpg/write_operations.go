package auditpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

func apiKey(id, name, prefix, kind string, revoked pgtype.Timestamptz) writeaudit.APIKey {
	key := writeaudit.APIKey{ID: id, Name: name, Prefix: prefix, Kind: kind}
	if revoked.Valid {
		value := revoked.Time
		key.RevokedAt = &value
	}
	return key
}

// GetResourceOwners returns each resource's recorded creator in request order.
// A resource created through a removed administrator copy reports that audit
// entry instead of a key.
func (s *Store) GetResourceOwners(ctx context.Context, tenantID, resourceType string, resourceIDs []string) ([]writeaudit.ResourceOwner, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return nil, writeaudit.ErrInvalidQuery
	}
	if err := writeaudit.ValidateOwnerQuery(resourceType, resourceIDs); err != nil {
		return nil, err
	}
	keys := make(map[string]writeaudit.APIKey, len(resourceIDs))
	admins := make(map[string]string)
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		rows, err := q.GetResourceOwners(ctx, sqlc.GetResourceOwnersParams{TenantID: tenant, ResourceType: resourceType, Column3: resourceIDs})
		if err != nil {
			return err
		}
		for _, row := range rows {
			keys[row.ResourceID] = apiKey(row.KeyID, row.KeyName, row.KeyPrefix, row.KeyKind, row.RevokedAt)
		}
		adminRows, err := q.GetAdminResourceOwners(ctx, sqlc.GetAdminResourceOwnersParams{TenantID: tenant, ResourceType: resourceType, Column3: resourceIDs})
		if err != nil {
			return err
		}
		for _, row := range adminRows {
			admins[row.ResourceID] = uuid.UUID(row.AuditID.Bytes).String()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]writeaudit.ResourceOwner, 0, len(resourceIDs))
	for _, id := range resourceIDs {
		owner := writeaudit.ResourceOwner{ResourceID: id}
		if key, ok := keys[id]; ok {
			owner.APIKey = &key
			source := "api_key"
			owner.Source = &source
		}
		if auditID, ok := admins[id]; ok && owner.APIKey == nil {
			source := "admin_copy"
			owner.Source = &source
			owner.AdminAuditID = &auditID
		}
		result = append(result, owner)
	}
	return result, nil
}

// ListWriteOperations pages a tenant's committed writes, newest first, with
// the ID as the tie breaker.
func (s *Store) ListWriteOperations(ctx context.Context, tenantID string, filter writeaudit.Filter) (writeaudit.Page, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return writeaudit.Page{}, writeaudit.ErrInvalidQuery
	}
	filter, err = filter.Validate()
	if err != nil {
		return writeaudit.Page{}, err
	}
	query := filter
	query.After, query.Limit = "", 0
	query.CreatedAfter, query.CreatedBefore = utc(query.CreatedAfter), utc(query.CreatedBefore)
	scope := cursorScope(struct {
		Tenant string
		Filter writeaudit.Filter
	}{uuid.UUID(tenant.Bytes).String(), query})
	params := sqlc.ListWriteOperationsParams{TenantID: tenant, KeyID: filter.KeyID, ResourceType: filter.ResourceType, ResourceID: filter.ResourceID, CreatedAfter: timestamp(filter.CreatedAfter), CreatedBefore: timestamp(filter.CreatedBefore), PageLimit: int32(filter.Limit + 1), AfterID: pgtype.UUID{Valid: true}}
	var rows []sqlc.ListWriteOperationsRow
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if filter.After != "" {
			id, ok := decodeCursor(filter.After, scope)
			if !ok {
				return writeaudit.ErrInvalidQuery
			}
			var err error
			params.AfterTime, err = q.GetWriteAuditCursor(ctx, sqlc.GetWriteAuditCursorParams{TenantID: tenant, ID: id})
			if errors.Is(err, pgx.ErrNoRows) {
				return writeaudit.ErrInvalidQuery
			}
			if err != nil {
				return err
			}
			params.AfterID = id
		}
		var err error
		rows, err = q.ListWriteOperations(ctx, params)
		return err
	})
	if err != nil {
		return writeaudit.Page{}, err
	}
	page := writeaudit.Page{Data: make([]writeaudit.Operation, 0, min(len(rows), filter.Limit)), HasMore: len(rows) > filter.Limit}
	if page.HasMore {
		rows = rows[:filter.Limit]
	}
	for _, row := range rows {
		page.Data = append(page.Data, writeaudit.Operation{ID: uuid.UUID(row.ID.Bytes).String(), Action: row.Action, ResourceType: row.ResourceType, ResourceID: row.ResourceID, ParentID: row.ParentID, RequestID: row.RequestID, TraceID: row.TraceID, APIKey: apiKey(row.KeyID, row.KeyName, row.KeyPrefix, row.KeyKind, row.RevokedAt), CreatedAt: row.CreatedAt.Time})
	}
	if page.HasMore {
		page.NextCursor = encodeCursor(page.Data[len(page.Data)-1].ID, scope)
	}
	return page, nil
}

// DeleteExpiredWriteOperations deletes at most limit operations older than
// olderThan in one transaction. It never removes an operation referenced by a
// genuine creation anchor, even after the resource is deleted.
func (s *Store) DeleteExpiredWriteOperations(ctx context.Context, olderThan time.Time, limit int) (int64, error) {
	if olderThan.IsZero() || limit < 1 || limit > 1000 {
		return 0, fmt.Errorf("auditpg: invalid retention batch of %d before %v", limit, olderThan)
	}
	var deleted int64
	err := s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		deleted, err = sqlc.New(tx).DeleteExpiredWriteOperations(ctx, sqlc.DeleteExpiredWriteOperationsParams{CreatedAt: pgtype.Timestamptz{Time: olderThan, Valid: true}, Limit: int32(limit)})
		return err
	})
	return deleted, err
}
