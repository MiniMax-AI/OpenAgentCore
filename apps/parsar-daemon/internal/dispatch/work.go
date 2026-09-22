package dispatch

import (
	"context"
	"sync"
)

// dispatchWork tracks admitted goroutines. Unlike sync.WaitGroup, its zero
// notification can be observed with a deadline without leaving a stale waiter
// behind when shutdown starts another cleanup attempt. Admission owns the rule
// that no new work may begin after a successful quiesce observation.
type dispatchWork struct {
	mu      sync.Mutex
	count   int
	settled chan struct{}
}

func (w *dispatchWork) Add(delta int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	next := w.count + delta
	if next < 0 {
		panic("dispatch: negative work count")
	}
	if w.count == 0 && next > 0 {
		w.settled = make(chan struct{})
	}
	w.count = next
	if next == 0 && w.settled != nil {
		close(w.settled)
		w.settled = nil
	}
}
func (w *dispatchWork) Done() { w.Add(-1) }
func (w *dispatchWork) Wait() { _ = w.waitContext(context.Background()) }
func (w *dispatchWork) waitContext(ctx context.Context) error {
	w.mu.Lock()
	settled := w.settled
	w.mu.Unlock()
	if settled == nil {
		return ctx.Err()
	}
	select {
	case <-settled:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
