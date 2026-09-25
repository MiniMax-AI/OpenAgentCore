package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type AuditAPIKey struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Kind      string     `json:"kind"`
	RevokedAt *time.Time `json:"revoked_at"`
}

type ResourceOwner struct {
	ResourceID   string       `json:"resource_id"`
	APIKey       *AuditAPIKey `json:"api_key"`
	Source       *string      `json:"source"`
	AdminAuditID *string      `json:"admin_audit_id"`
}

type WriteOperation struct {
	ID           string      `json:"id"`
	Action       string      `json:"action"`
	ResourceType string      `json:"resource_type"`
	ResourceID   string      `json:"resource_id"`
	ParentID     string      `json:"parent_id"`
	RequestID    string      `json:"request_id"`
	TraceID      string      `json:"trace_id"`
	APIKey       AuditAPIKey `json:"api_key"`
	CreatedAt    time.Time   `json:"created_at"`
}

type WriteOperationFilter struct {
	KeyID, ResourceType, ResourceID, After string
	CreatedAfter, CreatedBefore            *time.Time
	Limit                                  int
}

type WriteOperationPage struct {
	Data       []WriteOperation `json:"data"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor"`
}

func auditAPIKey(id, name, prefix, kind string, revoked pgtype.Timestamptz) AuditAPIKey {
	key := AuditAPIKey{ID: id, Name: name, Prefix: prefix, Kind: kind}
	if revoked.Valid {
		value := revoked.Time
		key.RevokedAt = &value
	}
	return key
}

func (s *Store) GetResourceOwners(ctx context.Context, tenantID, resourceType string, resourceIDs []string) ([]ResourceOwner, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	if !ValidAuditResourceType(resourceType) || len(resourceIDs) < 1 || len(resourceIDs) > 100 {
		return nil, fmt.Errorf("%w: invalid owner lookup", ErrInvalidInput)
	}
	for _, id := range resourceIDs {
		if !auditText(id, 256, true) {
			return nil, fmt.Errorf("%w: invalid resource ID", ErrInvalidInput)
		}
	}
	rows, err := s.queries.GetResourceOwners(ctx, sqlc.GetResourceOwnersParams{TenantID: tenant, ResourceType: resourceType, Column3: resourceIDs})
	if err != nil {
		return nil, err
	}
	keys := make(map[string]AuditAPIKey, len(rows))
	for _, row := range rows {
		keys[row.ResourceID] = auditAPIKey(row.KeyID, row.KeyName, row.KeyPrefix, row.KeyKind, row.RevokedAt)
	}
	adminRows, err := s.queries.GetAdminResourceOwners(ctx, sqlc.GetAdminResourceOwnersParams{TenantID: tenant, ResourceType: resourceType, Column3: resourceIDs})
	if err != nil {
		return nil, err
	}
	admins := make(map[string]string, len(adminRows))
	for _, row := range adminRows {
		admins[row.ResourceID] = uuid.UUID(row.AuditID.Bytes).String()
	}
	result := make([]ResourceOwner, 0, len(resourceIDs))
	for _, id := range resourceIDs {
		owner := ResourceOwner{ResourceID: id}
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

type writeAuditCursor struct{ ID, Scope string }

func auditCursorScope(tenant string, filter WriteOperationFilter) string {
	filter.After = ""
	filter.Limit = 0
	if filter.CreatedAfter != nil {
		value := filter.CreatedAfter.UTC()
		filter.CreatedAfter = &value
	}
	if filter.CreatedBefore != nil {
		value := filter.CreatedBefore.UTC()
		filter.CreatedBefore = &value
	}
	encoded, _ := json.Marshal(struct {
		Tenant string
		Filter WriteOperationFilter
	}{tenant, filter})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func auditTimestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func (s *Store) ListWriteOperations(ctx context.Context, tenantID string, filter WriteOperationFilter) (WriteOperationPage, error) {
	empty := WriteOperationPage{}
	tenant, err := parseID(tenantID)
	if err != nil {
		return empty, err
	}
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.ResourceType != "" && !ValidAuditResourceType(filter.ResourceType) || !auditText(filter.KeyID, 128, false) || !auditText(filter.ResourceID, 256, false) || filter.CreatedAfter != nil && filter.CreatedBefore != nil && !filter.CreatedAfter.Before(*filter.CreatedBefore) {
		return empty, fmt.Errorf("%w: invalid write operation filter", ErrInvalidInput)
	}
	scope := auditCursorScope(uuid.UUID(tenant.Bytes).String(), filter)
	params := sqlc.ListWriteOperationsParams{TenantID: tenant, KeyID: filter.KeyID, ResourceType: filter.ResourceType, ResourceID: filter.ResourceID, CreatedAfter: auditTimestamp(filter.CreatedAfter), CreatedBefore: auditTimestamp(filter.CreatedBefore), PageLimit: int32(filter.Limit + 1), AfterID: pgtype.UUID{Valid: true}}
	if filter.After != "" {
		encoded, decodeErr := base64.RawURLEncoding.DecodeString(filter.After)
		var cursor writeAuditCursor
		if len(filter.After) > 1024 || decodeErr != nil || json.Unmarshal(encoded, &cursor) != nil || cursor.Scope != scope {
			return empty, fmt.Errorf("%w: invalid write operation cursor", ErrInvalidInput)
		}
		id, parseErr := parseID(cursor.ID)
		if parseErr != nil {
			return empty, fmt.Errorf("%w: invalid write operation cursor", ErrInvalidInput)
		}
		params.AfterTime, err = s.queries.GetWriteAuditCursor(ctx, sqlc.GetWriteAuditCursorParams{TenantID: tenant, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, fmt.Errorf("%w: invalid write operation cursor", ErrInvalidInput)
		}
		if err != nil {
			return empty, err
		}
		params.AfterID = id
	}
	rows, err := s.queries.ListWriteOperations(ctx, params)
	if err != nil {
		return empty, err
	}
	page := WriteOperationPage{Data: make([]WriteOperation, 0, min(len(rows), filter.Limit)), HasMore: len(rows) > filter.Limit}
	if page.HasMore {
		rows = rows[:filter.Limit]
	}
	for _, row := range rows {
		page.Data = append(page.Data, WriteOperation{ID: uuid.UUID(row.ID.Bytes).String(), Action: row.Action, ResourceType: row.ResourceType, ResourceID: row.ResourceID, ParentID: row.ParentID, RequestID: row.RequestID, TraceID: row.TraceID, APIKey: auditAPIKey(row.KeyID, row.KeyName, row.KeyPrefix, row.KeyKind, row.RevokedAt), CreatedAt: row.CreatedAt.Time})
	}
	if page.HasMore {
		encoded, _ := json.Marshal(writeAuditCursor{ID: page.Data[len(page.Data)-1].ID, Scope: scope})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return page, nil
}

// DeleteExpiredWriteOperations bounds each retention transaction and never removes
// an operation referenced by a genuine creation anchor, even after resource deletion.
func (s *Store) DeleteExpiredWriteOperations(ctx context.Context, olderThan time.Time, limit int) (int64, error) {
	if olderThan.IsZero() || limit < 1 || limit > 1000 {
		return 0, fmt.Errorf("%w: invalid audit retention batch", ErrInvalidInput)
	}
	return s.queries.DeleteExpiredWriteOperations(ctx, sqlc.DeleteExpiredWriteOperationsParams{CreatedAt: pgtype.Timestamptz{Time: olderThan, Valid: true}, Limit: int32(limit)})
}
