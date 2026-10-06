package sessions

import "context"

// AdminRuntimeTarget names a live Session whose Runtime an administrator
// observes, with its tenant.
type AdminRuntimeTarget struct{ SessionID, TenantID string }

// AdminRuntimeTargetPage is one page of AdminRuntimeTargets.
type AdminRuntimeTargetPage struct {
	Data    []AdminRuntimeTarget
	HasMore bool
}

// AdminReader reads the administrator's cross-Project Session views.
type AdminReader interface {
	// ListAdminRuntimeTargets pages the live Sessions of the tenants by
	// creation time, then ID, ascending or descending, after the Session
	// after. A limit outside 1 to 100 or a malformed tenant is
	// ErrInvalidInput; an after Session that is malformed, deleted or
	// outside the tenants is ErrNotFound.
	ListAdminRuntimeTargets(ctx context.Context, tenants []string, after string, limit int, ascending bool) (AdminRuntimeTargetPage, error)
}
