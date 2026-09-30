// Package auditpg stores write and administrator audit records in PostgreSQL.
// Every other adapter records audit rows through it, inside its own business
// transaction; it is the one adapter that other adapters call directly.
package auditpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// The recorders run in the caller's business transaction: q must belong to it,
// and they never begin or commit one. An error aborts the business write. No
// request body or secret reaches a row.

// RecordWriteAudit records a public write in tenant and the resources the write
// genuinely created. Administrator provenance takes precedence. A write without
// provenance, such as internal lifecycle work, stays unattributed; malformed
// provenance fails closed with writeaudit.ErrInvalidSource. A request that
// already recorded its operation records nothing more.
func RecordWriteAudit(ctx context.Context, q *sqlc.Queries, tenant, action, resourceType, resourceID, parentID string, created ...writeaudit.Resource) error {
	if _, ok := adminaudit.FromContext(ctx); ok {
		return RecordAdminMutation(ctx, q, tenant, action, resourceType, resourceID)
	}
	source, ok := writeaudit.FromContext(ctx)
	if !ok {
		return nil
	}
	resources := append([]writeaudit.Resource{{Type: resourceType, ID: resourceID, ParentID: parentID}}, created...)
	if err := writeaudit.ValidateRecord(source, tenant, action, resources); err != nil {
		return err
	}
	tenantID, err := pgunit.ParseID(tenant)
	if err != nil {
		return writeaudit.ErrInvalidSource
	}
	id, err := q.InsertWriteAuditOperation(ctx, sqlc.InsertWriteAuditOperationParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenantID,
		KeyID: source.KeyID, KeyName: source.Name, KeyPrefix: source.Prefix, KeyKind: source.Kind,
		Action: action, ResourceType: resourceType, ResourceID: resourceID, ParentID: parentID, RequestID: source.RequestID, TraceID: source.TraceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, resource := range created {
		if err := q.InsertWriteAuditOwner(ctx, sqlc.InsertWriteAuditOwnerParams{TenantID: tenantID, ResourceType: resource.Type, ResourceID: resource.ID, ParentID: resource.ParentID, OperationID: id}); err != nil {
			return err
		}
	}
	return nil
}

// RecordAdminMutation records an administrator mutation in tenant, the Project
// that the administrator provenance names. Missing or malformed provenance
// fails with adminaudit.ErrInvalidSource.
func RecordAdminMutation(ctx context.Context, q *sqlc.Queries, tenant, action, resourceType, resourceID string) error {
	source, ok := adminaudit.FromContext(ctx)
	if !ok {
		return adminaudit.ErrInvalidSource
	}
	if err := source.ValidateProjectMutation(action, resourceType, resourceID); err != nil {
		return err
	}
	projectID, err := pgunit.ParseID(source.ProjectID)
	if err != nil {
		return adminaudit.ErrInvalidSource
	}
	tenantID, err := pgunit.ParseID(tenant)
	if err != nil {
		return adminaudit.ErrInvalidSource
	}
	// result_ids is retained for historical copy mappings; current writes record none.
	_, err = q.InsertAdminAudit(ctx, sqlc.InsertAdminAuditParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenantID, AdminCredentialID: source.CredentialID, ActorLabel: source.ActorLabel, Action: action, ProjectID: projectID, ResourceType: resourceType, ResourceID: resourceID, ResultIds: []byte(`[]`), RequestID: source.RequestID, TraceID: source.TraceID})
	return err
}

// RecordDeploymentMutation records a deployment-wide administrator mutation,
// which has no Project or tenant. Missing or malformed provenance fails with
// adminaudit.ErrInvalidSource.
func RecordDeploymentMutation(ctx context.Context, q *sqlc.Queries, action, resourceType, resourceID string) error {
	source, ok := adminaudit.FromContext(ctx)
	if !ok {
		return adminaudit.ErrInvalidSource
	}
	if err := source.ValidateDeploymentMutation(action, resourceType, resourceID); err != nil {
		return err
	}
	_, err := q.InsertAdminAudit(ctx, sqlc.InsertAdminAuditParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, AdminCredentialID: source.CredentialID, ActorLabel: source.ActorLabel, Action: action, ResourceType: resourceType, ResourceID: resourceID, ResultIds: []byte(`[]`), RequestID: source.RequestID, TraceID: source.TraceID})
	return err
}
