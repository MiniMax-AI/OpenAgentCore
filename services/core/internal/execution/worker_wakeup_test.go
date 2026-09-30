package execution

import (
	"sync"
	"testing"
)

func TestSchedulerWakeCoalescesConcurrentAdmissionsAndKeepsNextHint(t *testing.T) {
	worker := &Worker{scheduleWake: make(chan struct{}, 1)}
	var callers sync.WaitGroup
	for range 100 {
		callers.Go(func() {
			for range 100 {
				worker.wakeScheduler()
			}
		})
	}
	callers.Wait()
	if got := len(worker.scheduleWake); got != 1 {
		t.Fatalf("queued wakeups = %d, want one", got)
	}
	<-worker.scheduleWake
	// A commit while a previous scan is running needs a subsequent scan.
	worker.wakeScheduler()
	select {
	case <-worker.scheduleWake:
	default:
		t.Fatal("admission during a scan lost its wakeup")
	}
	select {
	case <-worker.scheduleWake:
		t.Fatal("coalesced submissions caused an extra scan")
	default:
	}
}
