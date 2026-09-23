// Package harnessconfig defines adapter-owned provider configuration support.
// Declarations describe a build, not a live Runtime or provider's readiness.
package harnessconfig

import (
	"errors"
	"slices"
)

// Provider is deliberately limited to configuration compatibility. Core owns
// credential admission and endpoint security; native launch options stay private.
type Provider struct {
	Protocol            string
	RequiresTokenLimits bool
}

type Configuration struct {
	Providers []Provider
}

func (c Configuration) Clone() Configuration {
	c.Providers = slices.Clone(c.Providers)
	return c
}

func (c Configuration) Provider(protocol string) (Provider, bool) {
	for _, provider := range c.Providers {
		if provider.Protocol == protocol {
			return provider, true
		}
	}
	return Provider{}, false
}

func (c Configuration) ValidateProtocol(protocol string) error {
	if _, ok := c.Provider(protocol); !ok {
		return errors.New("selected harness does not support this model provider protocol")
	}
	return nil
}

func (c Configuration) Validate(protocol string, contextWindow, maxOutputTokens int32) error {
	if err := c.ValidateProtocol(protocol); err != nil {
		return err
	}
	provider, _ := c.Provider(protocol)
	if provider.RequiresTokenLimits && (contextWindow <= 0 || maxOutputTokens <= 0) {
		return errors.New("selected harness requires positive model context_window and max_output_tokens")
	}
	return nil
}

// Registry is an immutable snapshot. Missing declarations remain unknown rather
// than inheriting another harness's configuration rules.
type Registry struct {
	configurations map[string]Configuration
}

func NewRegistry(configurations map[string]Configuration) Registry {
	registry := Registry{configurations: make(map[string]Configuration, len(configurations))}
	for kind, configuration := range configurations {
		registry.configurations[kind] = configuration.Clone()
	}
	return registry
}

func (r Registry) Lookup(kind string) (Configuration, bool) {
	configuration, ok := r.configurations[kind]
	return configuration.Clone(), ok
}

func (r Registry) ValidateProtocol(kind, protocol string) error {
	configuration, _ := r.Lookup(kind)
	return configuration.ValidateProtocol(protocol)
}

func (r Registry) Validate(kind, protocol string, contextWindow, maxOutputTokens int32) error {
	configuration, _ := r.Lookup(kind)
	return configuration.Validate(protocol, contextWindow, maxOutputTokens)
}

func (r Registry) SupportsProtocol(protocol string) bool {
	for _, configuration := range r.configurations {
		if _, ok := configuration.Provider(protocol); ok {
			return true
		}
	}
	return false
}
