package agent

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

var ErrUnsupportedKind = errors.New("agent: unsupported agent_kind")

// Registry keeps the daemon-advertised capability descriptor and execution
// factories for each agent_kind. Safe for concurrent use.
type Registry struct {
	mu             sync.RWMutex
	executors      map[string]ExecutorFactory
	views          map[string]View
	kinds          map[string]proto.SupportedAgentKind
	configurations map[string]harnessconfig.Configuration
}

func NewRegistry() *Registry {
	return &Registry{
		executors:      make(map[string]ExecutorFactory),
		views:          make(map[string]View),
		kinds:          make(map[string]proto.SupportedAgentKind),
		configurations: make(map[string]harnessconfig.Configuration),
	}
}

// SupportedAgentKinds returns the daemon-advertised capability
// descriptors sorted by kind so heartbeat payloads are stable.
func (r *Registry) SupportedAgentKinds() []proto.SupportedAgentKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]proto.SupportedAgentKind, 0, len(r.kinds))
	for _, info := range r.kinds {
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b proto.SupportedAgentKind) int {
		if a.Kind < b.Kind {
			return -1
		}
		if a.Kind > b.Kind {
			return 1
		}
		return 0
	})
	return out
}

func (r *Registry) ResolveExecutor(kind string) (ExecutorFactory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	factory := r.executors[kind]
	if factory == nil {
		return nil, fmt.Errorf("agent: executor unavailable for %q", kind)
	}
	return factory, nil
}

// Declaration returns an available kind's support, narrowed to what the
// Runtime advertises.
func (r *Registry) Declaration(kind string) (proto.Declaration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.kinds[kind].Available {
		return proto.Declaration{}, false
	}
	return r.configurations[kind].Declaration, true
}

// Configuration returns an owned declaration for registry wrappers. Wrappers
// transfer it with the factory; they must not infer configuration from kind names.
func (r *Registry) Configuration(kind string) (harnessconfig.Configuration, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	configuration, ok := r.configurations[kind]
	if !ok {
		return harnessconfig.Configuration{}, ErrUnsupportedKind
	}
	return configuration.Clone(), nil
}
