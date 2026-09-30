package vaults

import "fmt"

// Vault and Credential statuses.
const (
	StatusActive   = "active"
	StatusArchived = "archived"
)

// PageQuery selects one page of Vaults, or of one Vault's Credentials, ordered
// by creation time and then ID.
type PageQuery struct {
	// After is the last ID of the previous page, or empty for the first page.
	// An After that names no resource of the list is ErrNotFound.
	After     string
	Limit     int
	Ascending bool
	// Statuses filters by status; empty selects every status.
	Statuses []string
}

// Validate checks the page size and statuses and returns the statuses to
// select.
func (q PageQuery) Validate() ([]string, error) {
	if q.Limit < 1 || q.Limit > 100 {
		return nil, fmt.Errorf("%w: internal page size must be 1..100", ErrInvalidInput)
	}
	if len(q.Statuses) == 0 {
		return []string{StatusActive, StatusArchived}, nil
	}
	for _, status := range q.Statuses {
		if status != StatusActive && status != StatusArchived {
			return nil, fmt.Errorf("%w: invalid status", ErrInvalidInput)
		}
	}
	return q.Statuses, nil
}
