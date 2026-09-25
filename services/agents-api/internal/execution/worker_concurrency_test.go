package execution

import "testing"

func TestWorkerConfiguredConcurrencyReportsActualCapacity(t *testing.T) {
	for _, limit := range []int{1, 4, 7} {
		worker := &Worker{concurrency: limit}
		worker.observeSlots(limit)
		snapshot := worker.MetricsSnapshot()
		if *snapshot.SlotsTotal != int64(limit) || *snapshot.SlotsInUse != int64(limit) {
			t.Fatal("metrics do not reflect execution concurrency", snapshot)
		}
	}
	for _, invalid := range []int{-1, 1025} {
		// Reject before accessing the Store or acquiring its execution lease.
		if _, err := StartWorker(t.Context(), &Dispatcher{MaxConcurrentExecutions: invalid}); err == nil {
			t.Fatal("invalid concurrency accepted", invalid)
		}
	}
}
