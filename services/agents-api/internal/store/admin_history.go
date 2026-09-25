package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type AdminAuditFilter struct {
	ProjectID, ResourceType, ResourceID, Action, After string
	CreatedAfter, CreatedBefore                        *time.Time
	Limit                                              int
}

// AdminAuditOperation is one administrator write. ProjectID is null for
// deployment-wide writes, such as deployment default model providers.
type AdminAuditOperation struct {
	ID                string          `json:"id"`
	CreatedAt         time.Time       `json:"created_at"`
	AdminCredentialID string          `json:"admin_credential_id"`
	ActorLabel        string          `json:"actor_label"`
	Action            string          `json:"action"`
	ProjectID         *string         `json:"project_id" extensions:"x-nullable"`
	ResourceType      string          `json:"resource_type"`
	ResourceID        string          `json:"resource_id"`
	ResultIDs         json.RawMessage `json:"result_ids" swaggertype:"array,object"`
	RequestID         string          `json:"request_id"`
	TraceID           string          `json:"trace_id"`
}
type AdminAuditPage struct {
	Data       []AdminAuditOperation `json:"data"`
	HasMore    bool                  `json:"has_more"`
	NextCursor string                `json:"next_cursor"`
}

func (s *Store) ListAdminAudit(ctx context.Context, filter AdminAuditFilter) (AdminAuditPage, error) {
	page := AdminAuditPage{Data: []AdminAuditOperation{}}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit < 1 || filter.Limit > 100 || !auditText(filter.ProjectID, 128, false) || !auditText(filter.ResourceType, 64, false) || !auditText(filter.ResourceID, 256, false) || !auditText(filter.Action, 64, false) || filter.CreatedAfter != nil && filter.CreatedBefore != nil && !filter.CreatedAfter.Before(*filter.CreatedBefore) {
		return page, ErrInvalidInput
	}
	normalized := filter
	normalized.After = ""
	normalized.Limit = 0
	if normalized.CreatedAfter != nil {
		v := normalized.CreatedAfter.UTC()
		normalized.CreatedAfter = &v
	}
	if normalized.CreatedBefore != nil {
		v := normalized.CreatedBefore.UTC()
		normalized.CreatedBefore = &v
	}
	raw, _ := json.Marshal(normalized)
	digest := sha256.Sum256(raw)
	scope := hex.EncodeToString(digest[:])
	params := sqlc.ListAdminAuditLogParams{ProjectID: filter.ProjectID, ResourceType: filter.ResourceType, ResourceID: filter.ResourceID, Action: filter.Action, CreatedAfter: auditTimestamp(filter.CreatedAfter), CreatedBefore: auditTimestamp(filter.CreatedBefore), AfterID: pgtype.UUID{Valid: true}, PageLimit: int32(filter.Limit + 1)}
	if filter.After != "" {
		raw, err := base64.RawURLEncoding.DecodeString(filter.After)
		var cursor writeAuditCursor
		if len(filter.After) > 1024 || err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Scope != scope {
			return page, ErrInvalidInput
		}
		params.AfterID, err = parseID(cursor.ID)
		if err != nil {
			return page, ErrInvalidInput
		}
		params.AfterTime, err = s.queries.AdminAuditCursor(ctx, params.AfterID)
		if errors.Is(err, pgx.ErrNoRows) {
			return page, ErrInvalidInput
		}
		if err != nil {
			return page, err
		}
	}
	rows, err := s.queries.ListAdminAuditLog(ctx, params)
	if err != nil {
		return page, err
	}
	page.HasMore = len(rows) > filter.Limit
	if page.HasMore {
		rows = rows[:filter.Limit]
	}
	for _, row := range rows {
		page.Data = append(page.Data, AdminAuditOperation{ID: uuid.UUID(row.ID.Bytes).String(), CreatedAt: row.CreatedAt.Time, AdminCredentialID: row.AdminCredentialID, ActorLabel: row.ActorLabel, Action: row.Action, ProjectID: auditProjectID(row.ProjectID), ResourceType: row.ResourceType, ResourceID: row.ResourceID, ResultIDs: row.ResultIds, RequestID: row.RequestID, TraceID: row.TraceID})
	}
	if page.HasMore {
		raw, _ := json.Marshal(writeAuditCursor{ID: page.Data[len(page.Data)-1].ID, Scope: scope})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

func auditProjectID(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	value := uuid.UUID(id.Bytes).String()
	return &value
}
