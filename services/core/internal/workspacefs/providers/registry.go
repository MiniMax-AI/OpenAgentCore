// Package providers composes workspace filesystem adapters at process startup.
package providers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs/nfs"
)

// Registry uses the same registrations in Core and on nodes. Each registration
// implements every workspacefs.Adapter role; Core constructs Control from a
// credentialed record and nodes construct Resolver from public configuration.
type Registry struct {
	entries map[string]workspacefs.Adapter
}

func New() *Registry {
	return &Registry{entries: map[string]workspacefs.Adapter{nfs.Name: nfs.Registration{}}}
}

func (r *Registry) lookup(name string) (workspacefs.Adapter, error) {
	if r != nil {
		if value, ok := r.entries[name]; ok {
			return value, nil
		}
	}
	return nil, fmt.Errorf("%w: filesystem adapter is not registered", workspacefs.ErrUnsupported)
}

// Normalize canonicalizes a candidate configuration and encodes its credential input.
func (r *Registry) Normalize(c workspacefs.Configuration, credential json.RawMessage) (workspacefs.ConfigurationRecord, error) {
	if err := c.Validate(); err != nil {
		return workspacefs.ConfigurationRecord{}, err
	}
	a, err := r.lookup(c.Adapter)
	if err != nil {
		return workspacefs.ConfigurationRecord{}, err
	}
	record := workspacefs.ConfigurationRecord{Configuration: c}
	record.Parameters, record.Credential, err = a.Normalize(c.Parameters, credential)
	if err != nil {
		return workspacefs.ConfigurationRecord{}, err
	}
	return record, nil
}

// Control constructs an adapter from its canonical retained record.
// Construction performs no filesystem operation; Check owns live qualification.
func (r *Registry) Control(c workspacefs.ConfigurationRecord) (workspacefs.Control, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	a, err := r.lookup(c.Adapter)
	if err != nil {
		return nil, err
	}
	return a.Control(c)
}

// Resolve validates a transient binding with the public configuration only.
func (r *Registry) Resolve(ctx context.Context, binding workspacefs.Binding) (workspacefs.Directory, error) {
	if err := binding.Validate(); err != nil {
		return workspacefs.Directory{}, err
	}
	a, err := r.lookup(binding.Configuration.Adapter)
	if err != nil {
		return workspacefs.Directory{}, err
	}
	resolver, err := a.Resolver(binding.Configuration)
	if err != nil {
		return workspacefs.Directory{}, err
	}
	return resolver.Resolve(ctx, binding)
}
