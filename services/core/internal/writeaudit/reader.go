package writeaudit

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidQuery reports a provenance query with an invalid tenant, filter,
// page size or cursor.
var ErrInvalidQuery = errors.New("invalid write audit query")

// Reader reads write provenance as safe read models, never request bodies or
// credentials.
type Reader interface {
	GetResourceOwners(ctx context.Context, tenantID, resourceType string, resourceIDs []string) ([]ResourceOwner, error)
	ListWriteOperations(ctx context.Context, tenantID string, filter Filter) (Page, error)
}

// APIKey is the recorded identity of the key that made a write, with its
// current revocation time.
type APIKey struct {
	ID        string     `json:"id" binding:"required"`
	Name      string     `json:"name" binding:"required"`
	Prefix    string     `json:"prefix" binding:"required"`
	Kind      string     `json:"kind" enums:"issued" binding:"required"`
	RevokedAt *time.Time `json:"revoked_at" extensions:"x-nullable" binding:"required"`
}

// ResourceOwner is the recorded creator of one resource. APIKey is null for a
// resource without recorded creation provenance.
type ResourceOwner struct {
	ResourceID string  `json:"resource_id" binding:"required"`
	APIKey     *APIKey `json:"api_key" extensions:"x-nullable" binding:"required"`
}

// Operation is one committed write.
type Operation struct {
	ID           string       `json:"id" binding:"required"`
	Action       Action       `json:"action" binding:"required"`
	ResourceType ResourceType `json:"resource_type" binding:"required"`
	ResourceID   string       `json:"resource_id" binding:"required"`
	ParentID     string       `json:"parent_id" binding:"required"`
	RequestID    string       `json:"request_id" binding:"required"`
	TraceID      string       `json:"trace_id" binding:"required"`
	APIKey       APIKey       `json:"api_key" binding:"required"`
	CreatedAt    time.Time    `json:"created_at" binding:"required"`
}

// Filter selects committed writes, newest first. A zero Limit is the default
// page size.
type Filter struct {
	KeyID, ResourceType, ResourceID, After string
	CreatedAfter, CreatedBefore            *time.Time
	Limit                                  int
}

type Page struct {
	Data       []Operation `json:"data" binding:"required"`
	HasMore    bool        `json:"has_more" binding:"required"`
	NextCursor string      `json:"next_cursor" binding:"required"`
}

// Validate checks f and returns it with the default page size applied. The
// cursor is checked where it is resolved.
func (f Filter) Validate() (Filter, error) {
	if f.Limit == 0 {
		f.Limit = 20
	}
	if f.Limit < 1 || f.Limit > 100 || f.ResourceType != "" && !ValidResourceType(f.ResourceType) || !ValidText(f.KeyID, 128, false) || !ValidText(f.ResourceID, 256, false) || f.CreatedAfter != nil && f.CreatedBefore != nil && !f.CreatedAfter.Before(*f.CreatedBefore) {
		return Filter{}, ErrInvalidQuery
	}
	return f, nil
}

// ValidateOwnerQuery checks a batch creator lookup of one to 100 resources of
// one audited type.
func ValidateOwnerQuery(resourceType string, resourceIDs []string) error {
	if !ValidResourceType(resourceType) || len(resourceIDs) < 1 || len(resourceIDs) > 100 {
		return ErrInvalidQuery
	}
	for _, id := range resourceIDs {
		if !ValidText(id, 256, true) {
			return ErrInvalidQuery
		}
	}
	return nil
}
