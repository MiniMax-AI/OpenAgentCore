package sessions

import (
	"context"
	"time"
)

// AdminRuntimeTarget names a live Session whose Runtime an administrator
// observes, with its tenant.
type AdminRuntimeTarget struct{ SessionID, TenantID string }

// AdminRuntimeTargetPage is one page of AdminRuntimeTargets.
type AdminRuntimeTargetPage struct {
	Data    []AdminRuntimeTarget
	HasMore bool
}

// AdminSummaryFilter selects the Sessions created in [CreatedAfter,
// CreatedBefore); a nil bound is open.
type AdminSummaryFilter struct{ CreatedAfter, CreatedBefore *time.Time }

// AdminAssetCounts counts a tenant's assets.
type AdminAssetCounts struct {
	Agents               int64 `json:"agents"`
	Skills               int64 `json:"skills"`
	EnvironmentTemplates int64 `json:"environment_templates"`
	Files                int64 `json:"files"`
	Vaults               int64 `json:"vaults"`
	Credentials          int64 `json:"credentials"`
}

// AdminReader reads the administrator's cross-Project Session views.
type AdminReader interface {
	// ListAdminRuntimeTargets pages the live Sessions of the tenants by
	// creation time, then ID, ascending or descending, after the Session
	// after. A limit outside 1 to 100 or a malformed tenant is
	// ErrInvalidInput; an after Session that is malformed, deleted or
	// outside the tenants is ErrNotFound.
	ListAdminRuntimeTargets(ctx context.Context, tenants []string, after string, limit int, ascending bool) (AdminRuntimeTargetPage, error)
	// ReadAdminSummary counts the tenant's assets and visits each live Session
	// the filter selects, with GetSession's activity and the ID of the key that
	// created it, nil when none is recorded, from one read-only snapshot. A
	// malformed tenant is ErrInvalidInput; an error from visit ends the read.
	ReadAdminSummary(ctx context.Context, tenant string, filter AdminSummaryFilter, visit func(Session, *string) error) (AdminAssetCounts, error)
}
