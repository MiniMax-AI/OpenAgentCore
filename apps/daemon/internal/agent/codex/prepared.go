package codex

import "sync"

// Prepared owns a connected native resource until an Executor takes it.
// It observes owner cancellation and RPC exit, not continuous executor readiness.
type Prepared struct {
	mu                           sync.Mutex
	session                      *Session
	plan                         SessionPlan
	resumeID                     string
	requireExistingNativeSession bool
	started                      bool
	transferred                  chan struct{}
}

// Close waits for unused teardown and plan cleanup, including another caller's
// ongoing Close. After an Executor takes the resource it is inert; use
// Executor.Close to release it.
func (p *Prepared) Close() error {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	p.session.cancelFn()
	err := p.session.rpc.Close()
	if err == nil {
		p.plan.Cleanup()
	}
	return err
}

func (p *Prepared) watchOwner() {
	select {
	case <-p.session.cancelCtx.Done():
	case <-p.session.rpc.Done():
	case <-p.transferred:
		return
	}
	_ = p.Close()
}
