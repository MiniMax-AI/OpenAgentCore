package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
)

func TestWorkerMetricsUnknownAndDetached(t *testing.T) {
	worker := &Worker{}
	initial := worker.MetricsSnapshot()
	if initial.SlotsInUse != nil || initial.SlotsTotal != nil || initial.ExecutionOwner != nil || initial.Scheduler.Status != "unknown" || initial.Scheduler.LastRunAt != nil || initial.Scheduler.Processed != nil || initial.Scheduler.Failed != nil {
		t.Fatalf("unobserved worker reported values: %+v", initial)
	}
	worker.observeSlots(3)
	worker.observeOwnership(nil)
	worker.observeSchedulerPoll(2, nil)
	first := worker.MetricsSnapshot()
	if *first.SlotsInUse != 3 || *first.SlotsTotal != 4 || !*first.ExecutionOwner || *first.Scheduler.Processed != 2 || *first.Scheduler.Failed != 0 || first.Scheduler.Status != "ok" {
		t.Fatalf("unexpected observation: %+v", first)
	}
	observedAt := *first.Scheduler.LastRunAt
	*first.SlotsInUse, *first.SlotsTotal, *first.ExecutionOwner = 100, 100, false
	*first.Scheduler.Processed, *first.Scheduler.Failed = 100, 100
	*first.Scheduler.LastRunAt = time.Time{}
	second := worker.MetricsSnapshot()
	if *second.SlotsInUse != 3 || *second.SlotsTotal != 4 || !*second.ExecutionOwner || *second.Scheduler.Processed != 2 || *second.Scheduler.Failed != 0 || !second.Scheduler.LastRunAt.Equal(observedAt) {
		t.Fatal("caller mutation altered the worker's observations")
	}
	worker.observeSlots(1)
	worker.observeSchedulerPoll(0, nil)
	if *second.SlotsInUse != 3 || *second.Scheduler.Processed != 2 {
		t.Fatal("new observations altered a retained snapshot")
	}
}

// lostLease is an execution lease that no longer owns the database.
type lostLease struct{}

func (lostLease) CheckOwnership(context.Context) error { return pgunit.ErrLeaseClosed }
func (lostLease) CancelOperations(context.Context, context.CancelFunc) error {
	return pgunit.ErrLeaseClosed
}
func (lostLease) Close(context.Context) error { return nil }

func TestWorkerMetricsFailuresAndClosure(t *testing.T) {
	worker := &Worker{lease: lostLease{}}
	worker.observeOwnership(nil)
	if err := worker.CheckOwnership(t.Context()); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatalf("ownership error changed: %v", err)
	}
	if worker.MetricsSnapshot().ExecutionOwner != nil {
		t.Fatal("failed ownership check reported a known lease owner")
	}
	failure := errors.New("poll failed")
	worker.observeSchedulerPoll(3, failure)
	job := worker.MetricsSnapshot().Scheduler
	if job.Status != "failing" || job.Processed != nil || job.Failed == nil || *job.Failed != 1 || job.LastRunAt == nil {
		t.Fatalf("failed poll concealed unknown progress: %+v", job)
	}
	worker.observeWorkerStop(failure, context.Canceled)
	if worker.MetricsSnapshot().Scheduler.Status != "failing" {
		t.Fatal("concurrent cancellation concealed the worker failure")
	}
	worker.observeWorkerStop(fmt.Errorf("stop: %w", context.Canceled), context.Canceled)
	if worker.MetricsSnapshot().Scheduler.Status != "stopped" {
		t.Fatal("normal cancellation reported as a failure")
	}
	worker.observeWorkerClosed(nil)
	worker.observeOwnership(nil)
	closed := worker.MetricsSnapshot()
	if closed.ExecutionOwner == nil || *closed.ExecutionOwner || *closed.SlotsInUse != 0 {
		t.Fatal("late ownership observation revived a closed worker")
	}
	uncertain := &Worker{}
	uncertain.observeWorkerClosed(failure)
	if uncertain.MetricsSnapshot().ExecutionOwner != nil {
		t.Fatal("failed lease close reported certain ownership")
	}
}

func TestWorkerMetricsConcurrentSnapshots(t *testing.T) {
	worker := &Worker{}
	var running sync.WaitGroup
	for i := 0; i < 4; i++ {
		running.Add(1)
		go func() {
			defer running.Done()
			for j := 0; j < 100; j++ {
				worker.observeSlots(j % 5)
				worker.observeOwnership(nil)
				worker.observeSchedulerPoll(j%5, nil)
				value := worker.MetricsSnapshot()
				if *value.SlotsInUse < 0 || *value.SlotsInUse > *value.SlotsTotal || *value.SlotsTotal != 4 {
					t.Errorf("inconsistent slot snapshot: %+v", value)
				}
				*value.SlotsInUse = -1
				*value.ExecutionOwner = false
				*value.Scheduler.Processed = -1
			}
		}()
	}
	running.Wait()
}
