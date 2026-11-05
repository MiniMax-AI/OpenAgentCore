package agent

import (
	"fmt"
	"slices"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Registry keeps the daemon-advertised capability descriptor and execution
// factories for each agent_kind. Safe for concurrent use.
type Registry struct {
	mu           sync.RWMutex
	executors    map[string]ExecutorFactory
	kinds        map[string]proto.SupportedAgentKind
	declarations map[string]harnessconfig.Declaration
}

func NewRegistry() *Registry {
	return &Registry{
		executors:    make(map[string]ExecutorFactory),
		kinds:        make(map[string]proto.SupportedAgentKind),
		declarations: make(map[string]harnessconfig.Declaration),
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

// Declaration returns the declaration a kind was registered with.
func (r *Registry) Declaration(kind string) harnessconfig.Declaration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.declarations[kind]
}
