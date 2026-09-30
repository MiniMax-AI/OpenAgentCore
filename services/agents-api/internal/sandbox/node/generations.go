package node

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

type GenerationProvider struct {
	Generation          uint64
	SpecificationDigest string
	Provider            sandbox.SandboxProvider
	Probe               func(context.Context) error
	Close               func()
}

type GenerationManagerOptions struct {
	Initial []GenerationProvider
	Recover []sandbox.GenerationReference
	Collect []sandbox.GenerationReference
	Prepare func(context.Context, uint64, string) (GenerationProvider, error)
	Remove  func(context.Context, GenerationProvider) error
}

type localGeneration struct {
	value                GenerationProvider
	state, diagnostic    string
	refs                 int
	retryAt              time.Time
	failures             int
	removing             bool
	repairing, preparing bool
	collecting           bool
}

// GenerationManager owns local providers, preparation and all queued/in-flight
// references. Wire metadata is sparse; only correlated Core grants call Drop.
type GenerationManager struct {
	mu                                        sync.Mutex
	values                                    map[uint64]*localGeneration
	target                                    sandbox.NodeDeployment
	statusCursor, probeCursor, recoveryCursor uint64
	priorityProbe                             uint64
	options                                   GenerationManagerOptions
	ctx                                       context.Context
	cancel                                    context.CancelFunc
	preparingCancel                           context.CancelFunc
	wake                                      chan struct{}
	workers                                   sync.WaitGroup
}

func NewGenerationManager(ctx context.Context, options GenerationManagerOptions) (*GenerationManager, error) {
	if options.Prepare == nil || options.Remove == nil {
		return nil, sandbox.ErrInvalid
	}
	owned, cancel := context.WithCancel(ctx)
	m := &GenerationManager{values: map[uint64]*localGeneration{}, options: options, ctx: owned, cancel: cancel, wake: make(chan struct{}, 1)}
	for _, v := range options.Initial {
		if !validGeneration(v.Generation) || !validSpecificationDigest(v.SpecificationDigest) || sandbox.ValidateProvider(v.Provider) != nil || v.Probe == nil || m.values[v.Generation] != nil {
			cancel()
			return nil, sandbox.ErrInvalid
		}
		m.values[v.Generation] = &localGeneration{value: v, state: "preparing"}
	}
	for _, ref := range options.Recover {
		if !validGeneration(ref.Generation) || !validSpecificationDigest(ref.SpecificationDigest) || m.values[ref.Generation] != nil {
			cancel()
			return nil, sandbox.ErrInvalid
		}
		m.values[ref.Generation] = &localGeneration{value: GenerationProvider{Generation: ref.Generation, SpecificationDigest: ref.SpecificationDigest}, state: "failed", diagnostic: sandbox.NodeProviderUnavailable, repairing: true}
	}
	for _, ref := range options.Collect {
		if !validGeneration(ref.Generation) || !validSpecificationDigest(ref.SpecificationDigest) || m.values[ref.Generation] != nil {
			cancel()
			return nil, sandbox.ErrInvalid
		}
		m.values[ref.Generation] = &localGeneration{value: GenerationProvider{Generation: ref.Generation, SpecificationDigest: ref.SpecificationDigest}, collecting: true}
	}
	m.workers.Add(2)
	go func() { defer m.workers.Done(); m.prepareLoop() }()
	go func() { defer m.workers.Done(); m.probeLoop() }()
	return m, nil
}

func (m *GenerationManager) Close() {
	m.cancel()
	m.workers.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range m.values {
		if g.value.Close != nil {
			g.value.Close()
		}
	}
}

func (m *GenerationManager) Deployment(value sandbox.NodeDeployment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validGeneration(value.Generation) || !validSpecificationDigest(value.SpecificationDigest) || value.ServingGeneration != nil && (!validGeneration(*value.ServingGeneration) || *value.ServingGeneration > value.Generation) {
		return sandbox.ErrOwnership
	}
	if value.Generation < m.target.Generation {
		return nil
	}
	if g := m.values[value.Generation]; g != nil && (g.value.SpecificationDigest != value.SpecificationDigest || g.collecting) {
		return sandbox.ErrOwnership
	}
	if value.ServingGeneration != nil {
		if g := m.values[*value.ServingGeneration]; g != nil && g.collecting {
			return sandbox.ErrOwnership
		}
	}
	if value.Generation == m.target.Generation && value.SpecificationDigest != m.target.SpecificationDigest {
		return sandbox.ErrOwnership
	}
	if value.Generation != m.target.Generation && m.preparingCancel != nil {
		m.preparingCancel()
	}
	m.target = value
	if g := m.values[value.Generation]; g == nil {
		m.values[value.Generation] = &localGeneration{value: GenerationProvider{Generation: value.Generation, SpecificationDigest: value.SpecificationDigest}, state: "preparing"}
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}

func (m *GenerationManager) orderedLocked(after uint64) []uint64 {
	keys := make([]uint64, 0, len(m.values))
	for g, v := range m.values {
		if !v.removing {
			keys = append(keys, g)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	pivot := sort.Search(len(keys), func(i int) bool { return keys[i] > after })
	return append(keys[pivot:], keys[:pivot]...)
}

func (m *GenerationManager) Statuses() []sandbox.GenerationStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := []uint64{m.target.Generation}
	if m.target.ServingGeneration != nil {
		keys = append(keys, *m.target.ServingGeneration)
	}
	keys = append(keys, m.orderedLocked(m.statusCursor)...)
	result := make([]sandbox.GenerationStatus, 0, 8)
	seen := map[uint64]bool{}
	for _, key := range keys {
		g := m.values[key]
		if g == nil || g.removing || g.collecting || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, sandbox.GenerationStatus{Generation: key, SpecificationDigest: g.value.SpecificationDigest, State: g.state, Diagnostic: g.diagnostic})
		if key != m.target.Generation && (m.target.ServingGeneration == nil || key != *m.target.ServingGeneration) {
			m.statusCursor = key
		}
		if len(result) == 8 {
			break
		}
	}
	return result
}

func (m *GenerationManager) Retained(after uint64) []sandbox.GenerationReference {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := m.orderedLocked(after)
	result := make([]sandbox.GenerationReference, 0, 8)
	for _, key := range keys {
		g := m.values[key]
		result = append(result, sandbox.GenerationReference{Generation: key, SpecificationDigest: g.value.SpecificationDigest})
		if len(result) == 8 {
			break
		}
	}
	return result
}

// Acquire is called before queue admission, so queued work also prevents GC.
func (m *GenerationManager) Acquire(generation uint64) (sandbox.SandboxProvider, bool, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := m.values[generation]
	if g == nil || g.removing || g.collecting || g.preparing || g.value.Provider == nil {
		return nil, false, nil, ErrUnavailable
	}
	g.refs++
	var once sync.Once
	release := func() { once.Do(func() { m.mu.Lock(); g.refs--; m.mu.Unlock() }) }
	return g.value.Provider, g.state == "ready", release, nil
}

func (m *GenerationManager) Drop(ctx context.Context, grant sandbox.GenerationRetention) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if grant.Keep {
		return nil
	}
	m.mu.Lock()
	g := m.values[grant.Generation]
	if g == nil {
		m.mu.Unlock()
		return nil
	}
	if g.value.SpecificationDigest != grant.SpecificationDigest {
		m.mu.Unlock()
		return sandbox.ErrOwnership
	}
	quiet := true
	if provider, ok := g.value.Provider.(interface{ Quiescent() bool }); ok {
		quiet = provider.Quiescent()
	}
	if !quiet || g.refs != 0 || g.removing || m.target.Generation == grant.Generation || m.target.ServingGeneration != nil && *m.target.ServingGeneration == grant.Generation {
		m.mu.Unlock()
		return ErrUnavailable
	}
	g.collecting = true
	g.removing = true
	m.mu.Unlock()
	err := m.options.Remove(ctx, g.value)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		g.removing = false
		return err
	}
	if g.value.Close != nil {
		g.value.Close()
	}
	delete(m.values, grant.Generation)
	return nil
}

func (m *GenerationManager) prepareLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
		m.mu.Lock()
		var g *localGeneration
		if m.target.Generation != 0 {
			keys := []uint64{m.target.Generation}
			if m.target.ServingGeneration != nil {
				keys = append(keys, *m.target.ServingGeneration)
			}
			keys = append(keys, m.orderedLocked(m.recoveryCursor)...)
			for _, key := range keys {
				candidate := m.values[key]
				if candidate == nil || candidate.removing || candidate.collecting || candidate.preparing || candidate.refs != 0 || candidate.value.Provider != nil && !candidate.repairing || time.Now().Before(candidate.retryAt) {
					continue
				}
				if provider, ok := candidate.value.Provider.(interface{ Quiescent() bool }); ok && !provider.Quiescent() {
					continue
				}
				g = candidate
				m.recoveryCursor = key
				break
			}
		}
		if g == nil {
			m.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Minute)
		m.preparingCancel = cancel
		g.preparing = true
		g.refs++
		g.state = "preparing"
		g.diagnostic = ""
		generation, digest := g.value.Generation, g.value.SpecificationDigest
		m.mu.Unlock()
		value, err := m.options.Prepare(ctx, generation, digest)
		cancel()
		m.mu.Lock()
		m.preparingCancel = nil
		g.preparing = false
		g.refs--
		if err == nil && (value.Generation != generation || value.SpecificationDigest != digest || sandbox.ValidateProvider(value.Provider) != nil || value.Probe == nil) {
			err = sandbox.ErrOwnership
		}
		if err == nil {
			if g.value.Close != nil {
				g.value.Close()
			}
			g.value = value
			g.repairing = false
			g.failures = 0
			g.retryAt = time.Time{}
			g.state = "preparing"
		} else {
			if value.Close != nil {
				value.Close()
			}
			g.state = "failed"
			g.diagnostic = sandbox.NodeDiagnostic(err)
			g.failures++
			delays := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute}
			index := g.failures - 1
			if index >= len(delays) {
				index = len(delays) - 1
			}
			g.retryAt = time.Now().Add(delays[index])
		}
		m.mu.Unlock()
	}
}

func (m *GenerationManager) probeLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		m.mu.Lock()
		keys := m.orderedLocked(m.probeCursor)
		// Alternate priority probes with the fair retained-provider cursor. A large
		// history cannot postpone serving/target qualification or starve old owners.
		m.priorityProbe++
		priority := uint64(0)
		if m.priorityProbe%2 == 1 {
			priority = m.target.Generation
			if m.priorityProbe%4 == 3 && m.target.ServingGeneration != nil {
				priority = *m.target.ServingGeneration
			}
			keys = append([]uint64{priority}, keys...)
		}
		var g *localGeneration
		for _, key := range keys {
			value := m.values[key]
			if value != nil && value.value.Provider != nil && !value.removing && !value.collecting && !value.preparing && !value.repairing {
				g = value
				if key != priority {
					m.probeCursor = key
				}
				g.refs++
				break
			}
		}
		m.mu.Unlock()
		if g == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		err := g.value.Probe(ctx)
		cancel()
		m.mu.Lock()
		g.refs--
		if !errors.Is(err, context.Canceled) {
			g.state = "ready"
			g.diagnostic = ""
			if err != nil {
				g.state = "failed"
				g.diagnostic = sandbox.NodeDiagnostic(err)
				if errors.Is(err, sandbox.ErrRuntimeImageUnavailable) || errors.Is(err, sandbox.ErrMicrosandboxArtifactsUnavailable) {
					g.repairing = true
				}
			}
		}
		m.mu.Unlock()
	}
}

func (m *GenerationManager) Ready(generation uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := m.values[generation]
	return g != nil && !g.removing && g.value.Provider != nil && g.state == "ready"
}
