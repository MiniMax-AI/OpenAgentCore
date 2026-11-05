package automations

import (
	"errors"
	"testing"
	"time"
)

func at(s string) *time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return &t
}

func TestOccurrence(t *testing.T) {
	periodic := Automation{Type: Periodic, Status: Active, Expression: "0 * * * *", Timezone: "UTC", NextRunAt: at("2026-03-01T01:00:00Z")}
	now := *at("2026-03-01T05:10:00Z")
	if got := periodic.Occurrence(now); !got.ScheduledAt.Equal(*at("2026-03-01T05:00:00Z")) || got.Skipped || got.Next == nil || !got.Next.Equal(*at("2026-03-01T06:00:00Z")) {
		t.Errorf("after downtime only the latest occurrence is recorded: %+v", got)
	}
	periodic.Queued = true
	if got := periodic.Occurrence(now); !got.Skipped || got.Completed {
		t.Errorf("a queued invocation skips the occurrence: %+v", got)
	}
	once := Automation{Type: Once, Status: Active, NextRunAt: at("2026-03-01T01:00:00Z")}
	if got := once.Occurrence(now); got.Next != nil || got.Completed {
		t.Errorf("a one-time occurrence has no next: %+v", got)
	}
	once.Queued = true
	if got := once.Occurrence(now); !got.Skipped || !got.Completed {
		t.Errorf("a skipped one-time occurrence completes it: %+v", got)
	}
}

func TestPauseResume(t *testing.T) {
	now := *at("2026-03-01T00:00:00Z")
	var conflict *ConflictError
	if _, err := (Automation{Status: Completed}).Pause(); !errors.As(err, &conflict) {
		t.Errorf("pausing a completed automation: %v", err)
	}
	if paused, err := (Automation{Status: Paused}).Pause(); err != nil || paused.Status != Paused {
		t.Errorf("pausing a paused automation: %v", err)
	}
	if resumed, err := (Automation{Status: Active}).Resume(now); err != nil || resumed.Status != Active {
		t.Errorf("resuming an active automation: %v", err)
	}
	once := Automation{Type: Once, Status: Paused, RunAt: at("2026-02-01T00:00:00Z")}
	if _, err := once.Resume(now); !errors.As(err, &conflict) {
		t.Errorf("resuming a past run_at without a pending invocation: %v", err)
	}
	once.Pending = true
	if resumed, err := once.Resume(now); err != nil || resumed.Status != Active || resumed.NextRunAt != nil {
		t.Errorf("resuming with a pending invocation: %+v %v", resumed, err)
	}
	once.Pending, once.RunAt = false, at("2026-04-01T00:00:00Z")
	if resumed, err := once.Resume(now); err != nil || resumed.NextRunAt == nil {
		t.Errorf("resuming with a future run_at: %+v %v", resumed, err)
	}
}

func TestApply(t *testing.T) {
	now := *at("2026-03-01T00:00:00Z")
	var invalid *InvalidError
	var conflict *ConflictError
	periodic := Automation{Type: Periodic, Status: Active, Expression: "0 * * * *", Timezone: "UTC", TargetType: "existing_session"}
	if _, err := periodic.Apply(Patch{RunAt: at("2026-04-01T00:00:00Z")}, now); !errors.As(err, &invalid) || invalid.Param != "run_at" {
		t.Errorf("run_at on a periodic automation: %v", err)
	}
	if _, err := periodic.Apply(Patch{TargetType: "new_session", Target: []byte(`{}`)}, now); !errors.As(err, &invalid) || invalid.Param != "target.type" {
		t.Errorf("target of another type: %v", err)
	}
	updated, err := periodic.Apply(Patch{Expression: ptr("30 * * * *")}, now)
	if err != nil || updated.NextRunAt == nil || !updated.NextRunAt.Equal(*at("2026-03-01T00:30:00Z")) {
		t.Errorf("a new expression recomputes the next occurrence: %+v %v", updated, err)
	}
	once := Automation{Type: Once, Status: Active, RunAt: at("2026-05-01T00:00:00Z")}
	if _, err := once.Apply(Patch{Expression: ptr("0 * * * *")}, now); !errors.As(err, &invalid) || invalid.Param != "expression" {
		t.Errorf("expression on a one-time automation: %v", err)
	}
	if _, err := once.Apply(Patch{RunAt: at("2026-02-01T00:00:00Z")}, now); !errors.As(err, &invalid) {
		t.Errorf("past run_at: %v", err)
	}
	once.Pending = true
	if _, err := once.Apply(Patch{RunAt: at("2026-06-01T00:00:00Z")}, now); !errors.As(err, &conflict) {
		t.Errorf("run_at after the scheduled invocation exists: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
