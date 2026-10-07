package agent

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// ErrUnknownPermission is returned by PermissionResponder.SubmitPermission when
// the permID doesn't match any outstanding request. The router uses
// this to distinguish a benign race from a real forwarding failure.
var ErrUnknownPermission = errors.New("agent: unknown permission id")

// ErrUnknownAsk is returned by UserChoiceResponder.SubmitPromptForUserChoice when
// the askID doesn't match any outstanding ask. Same race semantics as
// ErrUnknownPermission.
var ErrUnknownAsk = errors.New("agent: unknown ask id")

// Registry keeps the daemon-advertised capability descriptor and execution
// factories for each agent_kind. Safe for concurrent use.
type Registry struct {
	mu             sync.RWMutex
	preparers      map[string]PreparationFactory
	executors      map[string]ExecutorFactory
	kinds          map[string]proto.SupportedAgentKind
	configurations map[string]harnessconfig.Configuration
}

func NewRegistry() *Registry {
	return &Registry{
		preparers:      make(map[string]PreparationFactory),
		executors:      make(map[string]ExecutorFactory),
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

func (r *Registry) ResolvePreparation(kind string) (PreparationFactory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f := r.preparers[kind]
	if f == nil {
		return nil, fmt.Errorf("agent: preparation unavailable for %q", kind)
	}
	return f, nil
}
