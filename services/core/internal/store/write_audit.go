package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// AuditResource identifies a resource genuinely created by the current transaction.
type AuditResource struct{ Type, ID, ParentID string }

// ValidAuditResourceType is shared by the write recorder and administrator queries.
func ValidAuditResourceType(value string) bool {
	switch value {
	case "agent", "session", "environment", "environment_template", "skill", "skill_version", "file", "vault", "credential", "artifact":
		return true
	}
	return false
}

func auditText(value string, max int, required bool) bool {
	return (!required || value != "") && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}

func validateWriteAuditSource(source writeaudit.Source, tenant string) error {
	actual, err := parseID(source.TenantID)
	expected, expectedErr := parseID(tenant)
	valid := err == nil && expectedErr == nil && actual == expected &&
		auditText(source.Name, 80, false) && auditText(source.RequestID, 128, true) && auditText(source.TraceID, 128, true)
	switch source.Kind {
	case "static", "console":
		digest := strings.TrimPrefix(source.KeyID, "static:")
		valid = valid && strings.HasPrefix(source.KeyID, "static:") && validProjectKeyDigest(digest) && source.Prefix == digest[:min(len(digest), 8)]
	case "issued":
		_, idErr := parseID(source.KeyID)
		valid = valid && idErr == nil && len(source.Prefix) == 11 && strings.HasPrefix(source.Prefix, "pc_")
		for _, c := range strings.TrimPrefix(source.Prefix, "pc_") {
			valid = valid && (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-')
		}
	default:
		valid = false
	}
	if !valid {
		return fmt.Errorf("%w: invalid write audit source", ErrInvalidInput)
	}
	return nil
}

// recordWriteAudit must use the caller's business transaction. Internal callers
// without a source stay unattributed; a malformed supplied source fails closed.
func recordWriteAudit(ctx context.Context, q *sqlc.Queries, tenant, action, resourceType, resourceID, parentID string, created ...AuditResource) error {
	if _, ok := adminaudit.FromContext(ctx); ok {
		return recordAdminMutation(ctx, q, tenant, action, resourceType, resourceID)
	}
	source, ok := writeaudit.FromContext(ctx)
	if !ok {
		return nil
	}
	if err := validateWriteAuditSource(source, tenant); err != nil {
		return err
	}
	switch action {
	case "create", "update", "delete", "send_events", "upload_file", "upload_version", "update_default_version":
	default:
		return fmt.Errorf("%w: invalid write audit action", ErrInvalidInput)
	}
	resources := append([]AuditResource{{Type: resourceType, ID: resourceID, ParentID: parentID}}, created...)
	for _, resource := range resources {
		if !ValidAuditResourceType(resource.Type) || !auditText(resource.ID, 256, true) || !auditText(resource.ParentID, 256, false) {
			return fmt.Errorf("%w: invalid write audit resource", ErrInvalidInput)
		}
	}
	tenantID, _ := parseID(tenant)
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
