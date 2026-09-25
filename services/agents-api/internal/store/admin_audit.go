package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// recordAdminMutation runs in the business transaction and never records request bodies or secrets.
func recordAdminMutation(ctx context.Context, q *sqlc.Queries, tenant, action, resourceType, resourceID string) error {
	source, ok := adminaudit.FromContext(ctx)
	if !ok || !auditText(source.CredentialID, 64, true) || !auditText(source.ActorLabel, 128, false) || !auditText(source.RequestID, 128, true) || !auditText(source.TraceID, 128, true) || !auditText(source.ProjectID, 128, true) || !auditText(action, 64, true) || !auditText(resourceType, 64, true) || !auditText(resourceID, 256, true) {
		return fmt.Errorf("%w: invalid administrator audit source", ErrInvalidInput)
	}
	projectID, err := parseID(source.ProjectID)
	if err != nil {
		return err
	}
	tenantID, err := parseID(tenant)
	if err != nil {
		return err
	}
	// result_ids is retained for historical copy mappings; current writes record none.
	_, err = q.InsertAdminAudit(ctx, sqlc.InsertAdminAuditParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenantID, AdminCredentialID: source.CredentialID, ActorLabel: source.ActorLabel, Action: action, ProjectID: projectID, ResourceType: resourceType, ResourceID: resourceID, ResultIds: []byte(`[]`), RequestID: source.RequestID, TraceID: source.TraceID})
	return err
}
