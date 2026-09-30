package execution

import "context"

// cancelLifecycles leaves the manager mutex free while the bounded lease gate
// drains. The cancellation itself runs synchronously under that gate; waiting
// for provider operations and lifecycle accounting happens only after release.
func (m *runtimeManager) cancelLifecycles(nodes []*runtimeNode) error {
	if len(nodes) == 0 {
		return nil
	}
	cancel := func() {
		for _, n := range nodes {
			n.lifecycle.cancelOperations()
		}
	}
	err := m.lease.CancelOperations(m.ctx, cancel)
	if err != nil {
		// A manual reconcile may have no running coordinator to consume failed.
		// Close admission synchronously; Worker shutdown still owns cancellation.
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		select {
		case m.failed <- err:
		default:
		}
	}
	return err
}

func (r *runtimeLifecycle) cancelOperations() {
	r.cancelMu.Lock()
	defer r.cancelMu.Unlock()
	r.stop()
	// A manual reconcile has an independently parented context. AfterFunc is
	// asynchronous, so it cannot be the only cancellation under the lease gate.
	if r.reconcileCancel != nil {
		r.reconcileCancel()
	}
}

func (r *runtimeLifecycle) beginReconcile(parent context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(parent)
	detach := context.AfterFunc(r.ctx, cancel)
	if err := r.lock(ctx); err != nil {
		detach()
		cancel()
		return nil, nil, err
	}
	r.cancelMu.Lock()
	if err := r.ctx.Err(); err != nil {
		r.cancelMu.Unlock()
		<-r.gate
		detach()
		cancel()
		return nil, nil, err
	}
	// The lifecycle gate admits only one active reconcile. Register cancellation
	// before its first leased query, and retain it until its last query returns.
	r.reconcileCancel = cancel
	r.cancelMu.Unlock()
	return ctx, func() {
		r.cancelMu.Lock()
		r.reconcileCancel = nil
		r.cancelMu.Unlock()
		<-r.gate
		detach()
		cancel()
	}, nil
}
