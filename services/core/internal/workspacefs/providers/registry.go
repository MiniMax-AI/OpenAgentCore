// Package providers composes workspace filesystem adapters at process startup.
package providers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs/nfs"
)

type adapter interface {
	workspacefs.Control
	workspacefs.Resolver
}

type registration struct {
	normalize func(json.RawMessage) (json.RawMessage, error)
	open      func(workspacefs.Configuration) (adapter, error)
}

// Registry uses the same registrations for control and node resolution. A
// registration supplies both protocol roles, never an optional side interface.
type Registry struct{ entries map[string]registration }

func New() *Registry {
	return &Registry{entries: map[string]registration{
		nfs.Name: {
			normalize: nfs.CanonicalParameters,
			open: func(c workspacefs.Configuration) (adapter, error) {
				return nfs.New(c)
			},
		},
	}}
}

func (r *Registry) lookup(name string) (registration, error) {
	if r != nil {
		if value, ok := r.entries[name]; ok {
			return value, nil
		}
	}
	return registration{}, fmt.Errorf("%w: filesystem adapter is not registered", workspacefs.ErrUnsupported)
}

func (r *Registry) Normalize(c workspacefs.Configuration) (workspacefs.Configuration, error) {
	if err := c.Validate(); err != nil {
		return workspacefs.Configuration{}, err
	}
	a, err := r.lookup(c.Adapter)
	if err != nil {
		return workspacefs.Configuration{}, err
	}
	c.Parameters, err = a.normalize(c.Parameters)
	if err != nil {
		return workspacefs.Configuration{}, err
	}
	return c, nil
}

// Control constructs an adapter from its canonical retained configuration.
// Construction performs no filesystem operation; Check owns live qualification.
func (r *Registry) Control(c workspacefs.Configuration) (workspacefs.Control, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	a, err := r.lookup(c.Adapter)
	if err != nil {
		return nil, err
	}
	return a.open(c)
}

func (r *Registry) Resolve(ctx context.Context, binding workspacefs.Binding) (workspacefs.Directory, error) {
	if err := binding.Validate(); err != nil {
		return workspacefs.Directory{}, err
	}
	a, err := r.lookup(binding.Configuration.Adapter)
	if err != nil {
		return workspacefs.Directory{}, err
	}
	instance, err := a.open(binding.Configuration)
	if err != nil {
		return workspacefs.Directory{}, err
	}
	return instance.Resolve(ctx, binding)
}
