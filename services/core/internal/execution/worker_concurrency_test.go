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
}
