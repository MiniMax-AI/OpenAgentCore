package execution

import (
	"errors"
	"sync"
	"time"
)

// WorkerMetrics contains only observations from this worker's existing operations.
// Missing pointers mean the corresponding operation has not established a value.
type WorkerMetrics struct {
	SlotsInUse     *int64
	SlotsTotal     *int64
	ExecutionOwner *bool
	Scheduler      WorkerJobMetrics
}

// WorkerJobMetrics describes the last completed scheduling poll. Failed counts
// failed polls, not failed Turns; Processed is unknown when a poll fails.
type WorkerJobMetrics struct {
	Status    string
	LastRunAt *time.Time
	Processed *int64
	Failed    *int64
}

type workerMetricsState struct {
	mu     sync.Mutex
	value  WorkerMetrics
	closed bool
}

// MetricsSnapshot copies bounded in-process observations without querying storage.
func (w *Worker) MetricsSnapshot() WorkerMetrics {
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()
	value := w.metrics.value
	value.SlotsInUse = copyMetric(value.SlotsInUse)
	value.SlotsTotal = copyMetric(value.SlotsTotal)
	value.ExecutionOwner = copyMetric(value.ExecutionOwner)
	value.Scheduler.LastRunAt = copyMetric(value.Scheduler.LastRunAt)
	value.Scheduler.Processed = copyMetric(value.Scheduler.Processed)
	value.Scheduler.Failed = copyMetric(value.Scheduler.Failed)
	if value.Scheduler.Status == "" {
		value.Scheduler.Status = "unknown"
	}
	return value
}

func copyMetric[T any](source *T) *T {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}

func (w *Worker) observeSlots(active int) {
	used, total := int64(active), int64(workerSlotLimit)
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()
	w.metrics.value.SlotsInUse = &used
	w.metrics.value.SlotsTotal = &total
}

func (w *Worker) observeOwnership(err error) {
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()
	if w.metrics.closed {
		return
	}
	w.metrics.value.ExecutionOwner = nil
	if err == nil {
		owned := true
		w.metrics.value.ExecutionOwner = &owned
	}
}

func (w *Worker) observeSchedulerPoll(processed int, err error) {
	now, handled, failed := time.Now().UTC(), int64(processed), int64(0)
	job := WorkerJobMetrics{Status: "ok", LastRunAt: &now, Processed: &handled, Failed: &failed}
	if err != nil {
		failed = 1
		job.Status, job.Processed = "failing", nil
	}
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()
	w.metrics.value.Scheduler = job
}

func (w *Worker) observeWorkerStop(runErr, contextErr error) {
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()
	w.metrics.value.Scheduler.Status = "stopped"
	if runErr != nil && (contextErr == nil || !errors.Is(runErr, contextErr)) {
		w.metrics.value.Scheduler.Status = "failing"
	}
}

func (w *Worker) observeWorkerClosed(err error) {
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()
	w.metrics.closed = true
	w.metrics.value.ExecutionOwner = nil
	if err == nil {
		owned := false
		w.metrics.value.ExecutionOwner = &owned
	}
	used := int64(0)
	w.metrics.value.SlotsInUse = &used
}
