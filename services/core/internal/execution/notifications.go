package execution

import "sync"

// executionNotifications carries process-local hints after durable commits.
// Every consumer rechecks storage and retains polling for external changes.
// Subscriptions exist only while a caller is waiting or a Turn is active.
type executionNotifications struct {
	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
}

func (n *executionNotifications) subscribe(tenant, session string) (<-chan struct{}, func()) {
	if n == nil {
		return nil, func() {}
	}
	key := tenant + ":" + session
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	if n.waiters == nil {
		n.waiters = make(map[string]map[chan struct{}]struct{})
	}
	if n.waiters[key] == nil {
		n.waiters[key] = make(map[chan struct{}]struct{})
	}
	n.waiters[key][ch] = struct{}{}
	n.mu.Unlock()
	return ch, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		delete(n.waiters[key], ch)
		if len(n.waiters[key]) == 0 {
			delete(n.waiters, key)
		}
	}
}

func (n *executionNotifications) notify(tenant, session string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.waiters[tenant+":"+session] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
