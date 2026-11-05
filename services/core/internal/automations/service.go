package automations

import (
	"context"
	"errors"
	"time"
)

// Storage saves Automations. Each write runs in one transaction that also
// records the write audit. A method returns ErrNotFound for an Automation the
// tenant does not own.
type Storage interface {
	Create(ctx context.Context, a Automation) (Automation, error)
	// Update locks the Automation, applies change to it and saves the result.
	Update(ctx context.Context, tenantID, id string, change func(Automation) (Automation, error)) (Automation, error)
	// Delete removes the Automation and its invocations.
	Delete(ctx context.Context, tenantID, id string) (string, error)
	// Invoke queues a manual invocation of the Automation's current target.
	Invoke(ctx context.Context, tenantID, id string) (Invocation, error)
	// Record records the due occurrences of active Automations, each in its own
	// transaction, and returns how many it recorded. A failing Automation does
	// not stop the others; the error joins the failures.
	Record(ctx context.Context, now time.Time) (int, error)
	// Deliverable returns at most limit queued invocations to deliver: for each
	// Automation, its oldest queued invocation that is manual or whose
	// Automation is active, least recently attempted first. It marks them
	// attempted. An invocation whose target cannot be opened is settled failed.
	Deliverable(ctx context.Context, limit int) ([]Delivery, error)
	// Settle settles a queued invocation and completes its one-time Automation
	// when the invocation is scheduled. An invocation that is no longer queued
	// or no longer exists is left alone.
	Settle(ctx context.Context, outcome Outcome) error
}

// ListQuery selects a page. SessionID filters Automations that target a Session.
type ListQuery struct {
	After     string
	Limit     int
	Ascending bool
	SessionID *string
}

// Page is a page of Automations.
type Page struct {
	Automations []Automation
	HasMore     bool
}

// InvocationPage is a page of invocations.
type InvocationPage struct {
	Invocations []Invocation
	HasMore     bool
}

// Reader reads Automations and their invocations. A list whose After names
// nothing returns ErrNotFound, and so does one of a missing Automation.
type Reader interface {
	Get(ctx context.Context, tenantID, id string) (Automation, error)
	List(ctx context.Context, tenantID string, query ListQuery) (Page, error)
	GetInvocation(ctx context.Context, tenantID, id, invocationID string) (Invocation, error)
	ListInvocations(ctx context.Context, tenantID, id string, query ListQuery) (InvocationPage, error)
}

// Service runs the Automation use cases; the storage runs the rest.
type Service struct{ Storage }

func NewService(storage Storage) (*Service, error) {
	if storage == nil {
		return nil, errors.New("automations: storage is required")
	}
	return &Service{storage}, nil
}

// Create saves an active Automation without invoking it.
func (s *Service) Create(ctx context.Context, a Automation) (Automation, error) {
	now := time.Now()
	var err error
	a.Status = Active
	if a.Type == Periodic {
		a.NextRunAt, err = a.nextRun(now)
	} else if a.NextRunAt = a.RunAt; !a.RunAt.After(now) {
		err = &InvalidError{"run_at", "run_at must be a future Unix timestamp."}
	}
	if err != nil {
		return Automation{}, err
	}
	return s.Storage.Create(ctx, a)
}

func (s *Service) Update(ctx context.Context, tenantID, id string, p Patch) (Automation, error) {
	return s.Storage.Update(ctx, tenantID, id, func(a Automation) (Automation, error) { return a.Apply(p, time.Now()) })
}

func (s *Service) Pause(ctx context.Context, tenantID, id string) (Automation, error) {
	return s.Storage.Update(ctx, tenantID, id, Automation.Pause)
}

func (s *Service) Resume(ctx context.Context, tenantID, id string) (Automation, error) {
	return s.Storage.Update(ctx, tenantID, id, func(a Automation) (Automation, error) { return a.Resume(time.Now()) })
}
