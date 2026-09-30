package adminaudit

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// ErrInvalidQuery reports an audit log query with an invalid filter, page size
// or cursor.
var ErrInvalidQuery = errors.New("invalid administrator audit query")

// Reader reads committed administrator mutations as safe metadata, never
// request bodies or secrets.
type Reader interface {
	ListAdminAudit(ctx context.Context, filter Filter) (Page, error)
}

// Filter selects committed administrator mutations, newest first. A zero Limit
// is the default page size.
type Filter struct {
	ProjectID, ResourceType, ResourceID, Action, After string
	CreatedAfter, CreatedBefore                        *time.Time
	Limit                                              int
}

// Operation is one administrator write. ProjectID is null for
// deployment-wide writes, such as deployment default model providers.
type Operation struct {
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

type Page struct {
	Data       []Operation `json:"data"`
	HasMore    bool        `json:"has_more"`
	NextCursor string      `json:"next_cursor"`
}

// Validate checks f and returns it with the default page size applied. The
// cursor is checked where it is resolved.
func (f Filter) Validate() (Filter, error) {
	if f.Limit == 0 {
		f.Limit = 50
	}
	if f.Limit < 1 || f.Limit > 100 || !writeaudit.ValidText(f.ProjectID, 128, false) || !writeaudit.ValidText(f.ResourceType, 64, false) || !writeaudit.ValidText(f.ResourceID, 256, false) || !writeaudit.ValidText(f.Action, 64, false) || f.CreatedAfter != nil && f.CreatedBefore != nil && !f.CreatedAfter.Before(*f.CreatedBefore) {
		return Filter{}, ErrInvalidQuery
	}
	return f, nil
}
