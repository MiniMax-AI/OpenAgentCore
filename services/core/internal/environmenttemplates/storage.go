package environmenttemplates

import "context"

// Storage saves Templates. Each method runs in one transaction that also
// records the write audit, so a failed audit leaves nothing changed. Update and
// Delete return ErrNotFound for a Template the tenant does not own.
type Storage interface {
	Create(ctx context.Context, tenantID string, in Input) (Template, error)
	Update(ctx context.Context, tenantID, templateID string, in Input) (Template, error)
	Delete(ctx context.Context, tenantID, templateID string) (string, error)
}
