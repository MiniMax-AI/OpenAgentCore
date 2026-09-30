package sandbox

import (
	"context"
	"math"
	"sync"

	"golang.org/x/sync/semaphore"
)

// CallFence excludes credential commits from provider calls, including helpers
// still completing after their HTTP caller's deadline. It never kills a helper
// or treats an unknown Create as absent. A timed-out fence leaves the key intact.
type CallFence struct {
	gate   *semaphore.Weighted
	once   sync.Once
	mu     sync.Mutex
	active int
	idle   chan struct{}
}

func (f *CallFence) init() { f.once.Do(func() { f.gate = semaphore.NewWeighted(math.MaxInt64) }) }
func (f *CallFence) Enter(ctx context.Context) (func(), error) {
	f.init()
	if err := f.gate.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { f.gate.Release(1) }, nil
}
func (f *CallFence) ChildStarted() func() {
	f.mu.Lock()
	if f.active == 0 {
		f.idle = make(chan struct{})
	}
	f.active++
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.active--
		if f.active == 0 {
			close(f.idle)
		}
		f.mu.Unlock()
	}
}
func (f *CallFence) Fence(ctx context.Context) (func(), error) {
	f.init()
	if err := f.gate.Acquire(ctx, math.MaxInt64); err != nil {
		return nil, err
	}
	release := func() { f.gate.Release(math.MaxInt64) }
	f.mu.Lock()
	idle, active := f.idle, f.active
	f.mu.Unlock()
	if active != 0 {
		select {
		case <-idle:
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	return release, nil
}
