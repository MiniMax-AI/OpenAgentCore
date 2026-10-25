package sessions

import (
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
	Agents               int64 `json:"agents" binding:"required"`
	Skills               int64 `json:"skills" binding:"required"`
	EnvironmentTemplates int64 `json:"environment_templates" binding:"required"`
	Files                int64 `json:"files" binding:"required"`
	Vaults               int64 `json:"vaults" binding:"required"`
	Credentials          int64 `json:"credentials" binding:"required"`
}
