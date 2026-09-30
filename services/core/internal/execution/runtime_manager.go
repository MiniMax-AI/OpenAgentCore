package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// runtimeManager owns node membership, not provider operations. Each registered
// node has one serial lifecycle; slow provider work cannot occupy another node.
var errRuntimeTransition = fmt.Errorf("%w: sandbox configuration is changing", ErrExecutionUnavailable)

type runtimeManager struct {
	store               *store.Store
	registry            *gateway.Registry
	config              RuntimeProvider
	setupInstallationID string
	loadDeployment      func(context.Context) (*RuntimeProvider, error)
	prepareDeployment   RuntimeDeploymentPreparer
	publishUnconfigured func(uint64)
	resetCursor         string
	resetRequestedAt    time.Time
	setupGate           chan struct{}
	mutationGate        chan struct{}
	switching           bool
	switchDrained       *deploymentDrain
	ctx                 context.Context
	cancel              context.CancelFunc
	mu                  sync.Mutex
	nodes               map[string]*runtimeNode
	running, closed     bool
	active              sync.WaitGroup
	failed              chan error
	inventory           chan struct{}
}

type runtimeNode struct {
	lifecycle *runtimeLifecycle
	started   bool // protected by runtimeManager.mu
	retiring  bool
}

// enter registers external callers before stop can begin draining. WaitGroup.Add
// and the closed check share the same mutex; shutdown never misses a new caller.
func (m *runtimeManager) enter(parent context.Context) (context.Context, func(), error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, ErrExecutionUnavailable
	}
	if m.switching {
		m.mu.Unlock()
		return nil, nil, errRuntimeTransition
	}
	m.active.Add(1)
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	detach := context.AfterFunc(m.ctx, cancel)
	if m.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { detach(); cancel(); m.active.Done() }, nil
}

func (m *runtimeManager) node(id string) (*runtimeNode, error) {
	m.mu.Lock()
	if m.switching {
		m.mu.Unlock()
		return nil, errRuntimeTransition
	}
	if m.closed || (m.loadDeployment != nil && m.config.Provider == nil) || (id == "") != (m.config.ProviderKind == "" || m.config.Mode == "direct") {
		m.mu.Unlock()
		return nil, ErrExecutionUnavailable
	}
	n := m.nodes[id]
	if n == nil {
		ctx, stop := context.WithCancel(m.ctx)
		n = &runtimeNode{lifecycle: &runtimeLifecycle{
			store: m.store, registry: m.registry, config: m.config, nodeID: id,
			gate: make(chan struct{}, 1), ctx: ctx, stop: stop,
			connections: make(map[string]*runtimeConnection), wakeHints: make(chan struct{}, 1),
		}}
		m.nodes[id] = n
	}
	if n.retiring {
		m.mu.Unlock()
		return nil, ErrExecutionUnavailable
	}
	start := m.running && !n.started
	if start {
		n.started = true
		m.active.Add(1)
	}
	m.mu.Unlock()
	if start {
		go m.runNode(n)
	}
	return n, nil
}

func (m *runtimeManager) hints(id string) chan<- struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	if n := m.nodes[id]; n != nil {
		return n.lifecycle.wakeHints
	}
	return nil
}

func (m *runtimeManager) syncNodes(ctx context.Context) ([]*runtimeNode, error) {
	ready, err := m.ensureDeployment(ctx)
	if err != nil || !ready {
		return nil, err
	}
	select {
	case m.inventory <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.ctx.Done():
		return nil, m.ctx.Err()
	}
	defer func() { <-m.inventory }()
	// A direct caller may add a newly registered node during the query. Only
	// entries present before this inventory snapshot can be retired by it.
	previous := m.snapshotNodes()
	ids, err := m.store.ListRuntimeLifecycleNodes(ctx)
	if err != nil {
		return nil, err
	}
	return m.applyInventory(previous, ids)
}

func (m *runtimeManager) snapshotNodes() map[string]*runtimeNode {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := make(map[string]*runtimeNode, len(m.nodes))
	for id, n := range m.nodes {
		previous[id] = n
	}
	return previous
}

func (m *runtimeManager) applyInventory(previous map[string]*runtimeNode, ids []string) ([]*runtimeNode, error) {
	nodes := make([]*runtimeNode, 0, len(ids))
	live := make(map[string]bool, len(ids))
	for _, id := range ids {
		n, err := m.node(id)
		if err != nil {
			return nil, err
		}
		live[id] = true
		nodes = append(nodes, n)
	}
	var retired []*runtimeNode
	m.mu.Lock()
	if m.switching {
		m.mu.Unlock()
		return nil, errRuntimeTransition
	}
	if m.closed {
		m.mu.Unlock()
		return nil, ErrExecutionUnavailable
	}
	for id, n := range previous {
		if !live[id] && m.nodes[id] == n && !n.retiring {
			n.retiring = true
			m.active.Add(1)
			retired = append(retired, n)
		}
	}
	m.mu.Unlock()
	// Inventory includes offline nodes. Only explicit removal retires a lane;
	// the Store requires all retained resources to be cleaned before removal.
	if err := m.cancelLifecycles(retired); err != nil {
		// No cancellation was authorized. Keep each retiring identity and gate;
		// acquiring its gate later cannot substitute for successful cancellation.
		// Undo only the retirement tasks that will not be started. Existing callers
		// and workers retain their accounting until ordinary owner shutdown settles.
		for range retired {
			m.active.Done()
		}
		return nil, err
	}
	for _, n := range retired {
		go m.retire(n)
	}
	return nodes, nil
}

func (m *runtimeManager) retire(n *runtimeNode) {
	defer m.active.Done()
	r := n.lifecycle
	// Retain the canceled entry until its last operation has actually returned;
	// a concurrent stale placement lookup cannot create an overlapping gate.
	r.gate <- struct{}{}
	<-r.gate
	m.mu.Lock()
	if m.nodes[r.nodeID] == n {
		delete(m.nodes, r.nodeID)
	}
	m.mu.Unlock()
}

func (m *runtimeManager) runNode(n *runtimeNode) {
	defer m.active.Done()
	r := n.lifecycle
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	err := runRuntimeMaintenance(r.ctx, ticker.C, r.wakeHints, r.reconcile)
	if err != nil && r.ctx.Err() == nil {
		// Provider failures are handled inside reconciliation. A failed owner
		// check or database scan stops the single execution owner as before.
		select {
		case m.failed <- err:
		default:
		}
	}
}

func (m *runtimeManager) run(ctx context.Context) error {
	m.mu.Lock()
	if m.closed || m.running {
		m.mu.Unlock()
		return ErrExecutionUnavailable
	}
	m.running = true
	m.mu.Unlock()
	if m.loadDeployment != nil {
		if err := m.store.CollectSandboxGenerations(ctx); err != nil {
			return err
		}
	}
	if err := m.resetStep(ctx); err != nil {
		return err
	}
	if _, err := m.syncNodes(ctx); err != nil && !errors.Is(err, errRuntimeTransition) {
		return err
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.ctx.Done():
			return m.ctx.Err()
		case err := <-m.failed:
			return err
		case <-ticker.C:
			if m.loadDeployment != nil {
				if err := m.store.CollectSandboxGenerations(ctx); err != nil {
					return err
				}
			}
			if err := m.resetStep(ctx); err != nil {
				return err
			}
			if _, err := m.syncNodes(ctx); err != nil && !errors.Is(err, errRuntimeTransition) {
				return err
			}
		}
	}
}

// Manual reconciliation waits for one page on each current node. It shares each
// node's gate but never controls the independent background maintenance clocks.
func (m *runtimeManager) reconcile(parent context.Context) error {
	ctx, finish, err := m.enter(parent)
	if err != nil {
		return err
	}
	defer finish()
	nodes, err := m.syncNodes(ctx)
	if err != nil {
		return err
	}
	results := make(chan error, len(nodes))
	var pending sync.WaitGroup
	for _, n := range nodes {
		pending.Add(1)
		go func() {
			defer pending.Done()
			results <- n.lifecycle.reconcile(ctx)
		}()
	}
	pending.Wait()
	close(results)
	for err := range results {
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *runtimeManager) stop() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.cancel()
}

// stop must precede drain. Both background loops and external provisioning or
// manual reconciliation finish before Worker releases its unique writer lease.
func (m *runtimeManager) drain() { m.active.Wait() }
