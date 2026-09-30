package projects

import (
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
)

// Project is a Core Project as administrators see it.
type Project struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	CreatedAt      time.Time  `json:"created_at"`
	ArchivedAt     *time.Time `json:"archived_at"`
	ActiveKeyCount int64      `json:"active_key_count"`
	TenantID       string     `json:"-"`
}

// Binding is a Project with the principal its API keys authenticate as.
type Binding struct {
	Project   Project
	Principal identity.Principal
}

// Page is one page of Projects ordered by ID.
type Page struct {
	Data    []Project `json:"data"`
	HasMore bool      `json:"has_more"`
}

// MaxListLimit bounds one page of Projects or API keys.
const MaxListLimit = 100

// ListQuery selects one page ordered by ID. After, when set, must name an
// existing entry of the list.
type ListQuery struct {
	After     string
	Limit     int
	Ascending bool
}

// Validate rejects a page size outside 1..MaxListLimit.
func (q ListQuery) Validate() error {
	if q.Limit < 1 || q.Limit > MaxListLimit {
		return ErrInvalidInput
	}
	return nil
}

// The execution scope and subject every new Project's keys authenticate as.
const organizationID = "core"

func newProject(id, name string) NewProject {
	return NewProject{ID: id, Name: name, OrganizationID: organizationID, ExternalProjectID: "proj_" + id, SubjectID: "project:" + id}
}
