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

var ErrUnsupportedKind = errors.New("agent: unsupported agent_kind")

// Registry maps agent_kind → Factory and keeps the daemon-advertised
// capability descriptor for each kind. Safe for concurrent use.
type Registry struct {
	mu             sync.RWMutex
	factories      map[string]Factory
	preparers      map[string]PreparationFactory
	executors      map[string]ExecutorFactory
	kinds          map[string]proto.SupportedAgentKind
	configurations map[string]harnessconfig.Configuration
}

func NewRegistry() *Registry {
	return &Registry{
		factories:      make(map[string]Factory),
		preparers:      make(map[string]PreparationFactory),
		executors:      make(map[string]ExecutorFactory),
		kinds:          make(map[string]proto.SupportedAgentKind),
		configurations: make(map[string]harnessconfig.Configuration),
	}
}

// Resolve returns the factory for kind, or wraps ErrUnsupportedKind.
func (r *Registry) Resolve(kind string) (Factory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.factories[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedKind, kind)
	}
	return f, nil
}

// Kinds returns the list of registered kinds sorted lexicographically.
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// SupportedAgentKinds returns the daemon-advertised capability
// descriptors sorted by kind so heartbeat payloads are stable.
func (r *Registry) SupportedAgentKinds() []proto.SupportedAgentKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]proto.SupportedAgentKind, 0, len(r.factories))
	for kind := range r.factories {
		info := r.kinds[kind]
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
