// Package automations owns Automations: saved schedules that deliver a target
// Session create or instructions, and the invocation records of each delivery.
// This package holds the rules; the PostgreSQL adapter stores them and the API
// package runs the deliveries.
package automations

import (
	"encoding/json"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
)

// Automation statuses and types.
const (
	Active    = "active"
	Paused    = "paused"
	Completed = "completed"

	Periodic = "periodic"
	Once     = "once"
)

// Automation is a saved schedule. Target is the safe projection of its target;
// Contents is the target request, set only on the way in, and the storage seals
// it.
type Automation struct {
	ID, TenantID string
	Type         string
	Name         string
	Metadata     map[string]string
	Status       string
	RunAt        *time.Time // once
	Expression   string     // periodic
	Timezone     string     // periodic
	// NextRunAt is the next occurrence to record while the Automation is active.
	NextRunAt  *time.Time
	TargetType string
	Target     json.RawMessage
	Contents   json.RawMessage
	CreatedAt  time.Time
	// Pending and Queued report a queued scheduled invocation and a queued
	// invocation of any source. Only locked reads fill them.
	Pending, Queued bool
}

// Invocation is the delivery record of one occurrence or manual invocation.
type Invocation struct {
	ID, AutomationID string
	CreatedAt        time.Time
	ScheduledAt      *time.Time
	// Snapshot is the Automation resource at the occurrence; nil for a manual
	// invocation.
	Snapshot                                     json.RawMessage
	Status, SkipReason, SessionID, Code, Message string
}

// Delivery is a queued invocation ready to deliver, with the Project that owns
// the Automation and the opened target request.
type Delivery struct {
	InvocationID, TargetType string
	Scope                    identity.Principal
	Request                  json.RawMessage
}

// Outcome settles a queued invocation. Status is completed or failed.
type Outcome struct {
	InvocationID, Status, SessionID, Code, Message string
}

// Patch is an update. Nil members keep the stored value. A target replaces the
// whole target and keeps its type.
type Patch struct {
	Name                 *string
	Metadata             map[string]string
	TargetType           string
	Target, Contents     json.RawMessage
	Expression, Timezone *string
	RunAt                *time.Time
}

// Occurrence is what a due Automation records.
type Occurrence struct {
	ScheduledAt time.Time
	// Skipped reports an occurrence recorded as skipped because an earlier
	// invocation is still queued.
	Skipped bool
	// Next is the Automation's next occurrence; nil for none.
	Next *time.Time
	// Completed reports a one-time Automation whose schedule has finished.
	Completed bool
}

// Occurrence decides the invocation of an active Automation whose NextRunAt is
// due. After downtime a periodic Automation records only its latest due
// occurrence.
func (a Automation) Occurrence(now time.Time) Occurrence {
	o := Occurrence{ScheduledAt: *a.NextRunAt, Skipped: a.Queued}
	if a.Type == Once {
		o.Completed = o.Skipped
		return o
	}
	schedule, err := ParseSchedule(a.Expression, a.Timezone)
	for err == nil {
		next, ok := schedule.Next(o.ScheduledAt)
		if !ok {
			break
		}
		if next.After(now) {
			o.Next = &next
			break
		}
		o.ScheduledAt = next
	}
	return o
}

// nextRun is the first occurrence of a periodic Automation after now.
func (a Automation) nextRun(now time.Time) (*time.Time, error) {
	schedule, err := ParseSchedule(a.Expression, a.Timezone)
	if err != nil {
		return nil, err
	}
	next, _ := schedule.Next(now)
	return &next, nil
}

// Pause stops scheduled delivery. A paused Automation stays paused.
func (a Automation) Pause() (Automation, error) {
	if a.Status == Completed {
		return a, &ConflictError{"A completed automation cannot be paused."}
	}
	a.Status = Paused
	return a, nil
}

// Resume restarts scheduled delivery. A one-time Automation without a pending
// scheduled invocation needs a future run_at. An active Automation is unchanged.
func (a Automation) Resume(now time.Time) (Automation, error) {
	var err error
	switch {
	case a.Status == Completed:
		return a, &ConflictError{"A completed automation cannot be resumed."}
	case a.Status == Active:
		return a, nil
	case a.Type == Periodic:
		a.NextRunAt, err = a.nextRun(now)
	case a.Pending:
		a.NextRunAt = nil
	case a.RunAt.After(now):
		a.NextRunAt = a.RunAt
	default:
		return a, &ConflictError{"A one-time automation without a pending invocation needs a future run_at to resume."}
	}
	a.Status = Active
	return a, err
}

// Apply applies an update. Timing fields must match the stored type, run_at
// must be in the future and cannot change once the scheduled invocation exists,
// and an active Automation recomputes its next occurrence.
func (a Automation) Apply(p Patch, now time.Time) (Automation, error) {
	if p.Name != nil {
		a.Name = *p.Name
	}
	if p.Metadata != nil {
		a.Metadata = p.Metadata
	}
	if p.Target != nil {
		if p.TargetType != a.TargetType {
			return a, &InvalidError{"target.type", "target.type must remain " + a.TargetType + "."}
		}
		a.Target, a.Contents = p.Target, p.Contents
	}
	var err error
	switch {
	case a.Type == Once && (p.Expression != nil || p.Timezone != nil):
		param := "expression"
		if p.Expression == nil {
			param = "timezone"
		}
		return a, &InvalidError{param, param + " applies only to periodic automations."}
	case a.Type == Periodic && p.RunAt != nil:
		return a, &InvalidError{"run_at", "run_at applies only to one-time automations."}
	case p.RunAt != nil:
		if !p.RunAt.After(now) {
			return a, &InvalidError{"run_at", "run_at must be a future Unix timestamp."}
		}
		if a.Status == Completed || a.Pending {
			return a, &ConflictError{"run_at cannot change once the scheduled invocation exists."}
		}
		a.RunAt = p.RunAt
		if a.Status == Active {
			a.NextRunAt = p.RunAt
		}
	case p.Expression != nil || p.Timezone != nil:
		if p.Expression != nil {
			a.Expression = *p.Expression
		}
		if p.Timezone != nil {
			a.Timezone = *p.Timezone
		}
		var next *time.Time
		if next, err = a.nextRun(now); err == nil && a.Status == Active {
			a.NextRunAt = next
		}
	}
	return a, err
}

// Resource projects the Automation onto its public resource.
func (a Automation) Resource() v1.Automation {
	resource := v1.Automation{Type: a.Type, ID: a.ID, Object: "agent.automation", CreatedAt: a.CreatedAt.Unix(), Name: a.Name, Target: a.Target, Metadata: a.Metadata, CreatedBy: v1.AutomationCreatedBy{Type: "developer"}, Status: a.Status}
	if a.Type == Periodic {
		resource.Expression, resource.Timezone = &a.Expression, &a.Timezone
	} else {
		runAt := a.RunAt.Unix()
		resource.RunAt = &runAt
	}
	return resource
}

// Resource projects the invocation onto its public resource.
func (i Invocation) Resource() v1.AutomationInvocation {
	resource := v1.AutomationInvocation{ID: i.ID, Object: "agent.automation.invocation", AutomationID: i.AutomationID, CreatedAt: i.CreatedAt.Unix(), Source: v1.AutomationInvocationSource{Type: "manual"}, Status: i.Status}
	if i.ScheduledAt != nil {
		scheduled := i.ScheduledAt.Unix()
		resource.ScheduledAt = &scheduled
		resource.Source = v1.AutomationInvocationSource{Type: "automation", Automation: i.Snapshot}
	}
	if i.SessionID != "" {
		resource.SessionID = &i.SessionID
	}
	if i.SkipReason != "" {
		resource.SkipReason = &i.SkipReason
	}
	if i.Status == "failed" {
		resource.Error = &v1.AutomationInvocationError{Code: i.Code, Message: i.Message}
	}
	return resource
}
