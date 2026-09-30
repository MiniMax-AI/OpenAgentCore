package execution

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type maintenanceTestLoop struct {
	t        *testing.T
	ticks    chan time.Time
	hints    chan struct{}
	entered  chan int
	release  chan struct{}
	done     chan error
	finished chan struct{}
	cancel   context.CancelFunc
	calls    atomic.Int32
}

func newMaintenanceTestLoop(t *testing.T, queuedHint bool, result func(int) error) *maintenanceTestLoop {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	loop := &maintenanceTestLoop{
		t: t, ticks: make(chan time.Time, 1), hints: make(chan struct{}, 1),
		entered: make(chan int, 1), release: make(chan struct{}),
		done: make(chan error, 1), finished: make(chan struct{}), cancel: cancel,
	}
	if queuedHint {
		loop.hints <- struct{}{}
	}
	go func() {
		defer close(loop.finished)
		loop.done <- runRuntimeMaintenance(ctx, loop.ticks, loop.hints, func(ctx context.Context) error {
			call := int(loop.calls.Add(1))
			loop.entered <- call
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-loop.release:
			}
			if result != nil {
				return result(call)
			}
			return nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-loop.finished:
		case <-time.After(2 * time.Second):
			t.Error("maintenance loop did not stop")
		}
	})
	return loop
}

func (loop *maintenanceTestLoop) expectScan(want int) {
	loop.t.Helper()
	select {
	case got := <-loop.entered:
		if got != want {
			loop.t.Fatalf("scan = %d, want %d", got, want)
		}
	case err := <-loop.done:
		loop.t.Fatalf("maintenance stopped before scan %d: %v", want, err)
	case <-time.After(2 * time.Second):
		loop.t.Fatalf("maintenance did not enter scan %d", want)
	}
}

func (loop *maintenanceTestLoop) finishScan() {
	loop.t.Helper()
	select {
	case loop.release <- struct{}{}:
	case <-time.After(2 * time.Second):
		loop.t.Fatal("maintenance did not release its current scan")
	}
}

func (loop *maintenanceTestLoop) expectIdle() {
	loop.t.Helper()
	// A short negative assertion catches immediate extra scans. Positive ordering
	// is controlled by callback barriers and manual ticks, never wall-clock ticks.
	select {
	case got := <-loop.entered:
		loop.t.Fatalf("unexpected scan %d without an eligible trigger", got)
	case err := <-loop.done:
		loop.t.Fatalf("maintenance stopped while idle: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
}

func (loop *maintenanceTestLoop) hintBurst(count int) int {
	accepted := 0
	for range count {
		select {
		case loop.hints <- struct{}{}:
			accepted++
		default:
		}
	}
	return accepted
}

func (loop *maintenanceTestLoop) tick() {
	loop.t.Helper()
	select {
	case loop.ticks <- time.Now():
	default:
		loop.t.Fatal("previous manual tick was not consumed")
	}
}

func (loop *maintenanceTestLoop) expectResult(want error) {
	loop.t.Helper()
	select {
	case got := <-loop.done:
		if !errors.Is(got, want) {
			loop.t.Fatalf("maintenance error = %v, want %v", got, want)
		}
	case <-time.After(2 * time.Second):
		loop.t.Fatal("maintenance did not return")
	}
}

func TestRuntimeMaintenanceIdleKeepsOnlyNormalScans(t *testing.T) {
	loop := newMaintenanceTestLoop(t, false, nil)
	loop.expectScan(1)
	loop.finishScan()
	loop.expectIdle()
	for want := 2; want <= 4; want++ {
		loop.tick()
		loop.expectScan(want)
		loop.finishScan()
		loop.expectIdle()
	}
	loop.cancel()
	loop.expectResult(context.Canceled)
}

func TestRuntimeMaintenanceStartupAbsorbsQueuedHint(t *testing.T) {
	loop := newMaintenanceTestLoop(t, true, nil)
	loop.expectScan(1)
	if len(loop.hints) != 0 {
		t.Fatal("startup did not absorb the hint before scanning")
	}
	loop.finishScan()
	loop.expectIdle()
	if loop.hintBurst(1) != 1 {
		t.Fatal("fresh hint was not queued")
	}
	loop.expectScan(2)
	loop.finishScan()
	loop.expectIdle()
}

func TestRuntimeMaintenanceHintBurstHasOneExtraPerPeriod(t *testing.T) {
	loop := newMaintenanceTestLoop(t, false, nil)
	loop.expectScan(1)
	for period := 0; period < 4; period++ {
		// The normal scan is blocked while a burst arrives. Its hint must survive
		// completion and authorize exactly one additional scan in this period.
		if accepted := loop.hintBurst(10000); accepted != 1 {
			t.Fatalf("burst accepted %d queued hints, want 1", accepted)
		}
		loop.finishScan()
		loop.expectScan(2 + 2*period)
		if accepted := loop.hintBurst(10000); accepted != 1 {
			t.Fatalf("busy extra scan accepted %d queued hints, want 1", accepted)
		}
		loop.finishScan()
		loop.expectIdle()
		// Another burst after the extra scan cannot create another allowance.
		if accepted := loop.hintBurst(10000); accepted != 0 {
			t.Fatalf("spent-period hint was consumed early: accepted %d", accepted)
		}
		loop.expectIdle()
		loop.tick()
		loop.expectScan(3 + 2*period)
		if len(loop.hints) != 0 {
			t.Fatal("normal tick did not absorb the prior queued hint")
		}
	}
	loop.finishScan()
	loop.expectIdle()
	if got := loop.calls.Load(); got != 9 {
		t.Fatalf("four periods produced %d scans, want startup + 4 extra + 4 normal", got)
	}
}

func TestRuntimeMaintenanceSimultaneousTickAndHintUseOneNormalScan(t *testing.T) {
	// Both triggers are ready before the callback barrier opens. Repeat to cover
	// selection variability without making assertions depend on either outcome.
	for attempt := 0; attempt < 20; attempt++ {
		loop := newMaintenanceTestLoop(t, false, nil)
		loop.expectScan(1)
		loop.hintBurst(1)
		loop.tick()
		loop.finishScan()
		loop.expectScan(2)
		if len(loop.ticks) != 0 || len(loop.hints) != 0 {
			t.Fatal("simultaneous triggers were not merged before the normal scan")
		}
		loop.finishScan()
		loop.expectIdle()
		// The merged scan was normal, so this period still permits one extra.
		loop.hintBurst(1)
		loop.expectScan(3)
		loop.finishScan()
		loop.cancel()
		loop.expectResult(context.Canceled)
	}
}

func TestRuntimeMaintenancePreservesHintArrivingDuringNormalScan(t *testing.T) {
	loop := newMaintenanceTestLoop(t, false, nil)
	loop.expectScan(1)
	loop.finishScan()
	loop.expectIdle()
	loop.tick()
	loop.expectScan(2)
	loop.hintBurst(1)
	loop.finishScan()
	loop.expectScan(3)
	loop.finishScan()
	loop.expectIdle()
}

func TestRuntimeMaintenanceCancellationPrecedesNewScan(t *testing.T) {
	t.Run("before startup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		ticks := make(chan time.Time, 1)
		hints := make(chan struct{}, 1)
		ticks <- time.Now()
		hints <- struct{}{}
		calls := 0
		err := runRuntimeMaintenance(ctx, ticks, hints, func(context.Context) error {
			calls++
			return nil
		})
		if !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("cancelled startup: calls=%d, error=%v", calls, err)
		}
	})
	t.Run("busy scan with queued triggers", func(t *testing.T) {
		loop := newMaintenanceTestLoop(t, false, nil)
		loop.expectScan(1)
		loop.hintBurst(1)
		loop.tick()
		loop.cancel()
		loop.expectResult(context.Canceled)
		if got := loop.calls.Load(); got != 1 {
			t.Fatalf("cancellation admitted another scan: %d", got)
		}
	})
}

func TestRuntimeMaintenanceReturnsReconcileError(t *testing.T) {
	failure := errors.New("controlled reconciliation failure")
	for _, failAt := range []int{1, 2} {
		t.Run(map[int]string{1: "startup", 2: "hint"}[failAt], func(t *testing.T) {
			loop := newMaintenanceTestLoop(t, false, func(call int) error {
				if call == failAt {
					return failure
				}
				return nil
			})
			loop.expectScan(1)
			if failAt == 2 {
				loop.finishScan()
				loop.hintBurst(1)
				loop.expectScan(2)
			}
			// An already pending normal tick cannot retry a failed callback.
			loop.tick()
			loop.finishScan()
			loop.expectResult(failure)
			if got := loop.calls.Load(); got != int32(failAt) {
				t.Fatalf("failure triggered another scan: %d", got)
			}
		})
	}
}

func TestRuntimeMaintenanceContinuousHintStormKeepsPeriodLimit(t *testing.T) {
	loop := newMaintenanceTestLoop(t, false, nil)
	loop.expectScan(1)
	stopStorm := make(chan struct{})
	stormDone := make(chan struct{})
	stormStarted := make(chan struct{})
	go func() {
		defer close(stormDone)
		loop.hintBurst(1)
		close(stormStarted)
		for {
			select {
			case <-stopStorm:
				return
			default:
			}
			loop.hintBurst(1)
			runtime.Gosched()
		}
	}()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() { close(stopStorm) })
		<-stormDone
	}
	t.Cleanup(stop)
	<-stormStarted
	for period := 0; period < 3; period++ {
		loop.finishScan()
		loop.expectScan(2 + 2*period)
		loop.finishScan()
		loop.expectIdle()
		if period < 2 {
			loop.tick()
			loop.expectScan(3 + 2*period)
		}
	}
	stop()
	if got := loop.calls.Load(); got != 6 {
		t.Fatalf("continuous hint storm produced %d scans, want 3 normal and 3 extra", got)
	}
	loop.cancel()
	loop.expectResult(context.Canceled)
}

func TestRuntimeMaintenanceCancellationAfterSuccessfulScanWinsReadyTriggers(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticks := make(chan time.Time, 1)
	hints := make(chan struct{}, 1)
	calls := 0
	unwantedScan := errors.New("scan started after cancellation")
	err := runRuntimeMaintenance(ctx, ticks, hints, func(context.Context) error {
		calls++
		if calls > 1 {
			return unwantedScan
		}
		ticks <- time.Now()
		hints <- struct{}{}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("ready triggers bypassed cancellation: calls=%d, error=%v", calls, err)
	}
}
