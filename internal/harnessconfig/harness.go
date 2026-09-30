// Package harnessconfig defines the shared model configuration contract.
// This file is the authoring entry point for Harness configuration: declare
// native provider support and parameter validation once, then bind that same
// Configuration when registering the Runtime Harness. Core admission, support
// descriptions and every Runtime execution/preparation entry consume it.
// Adapter-specific validation and native rendering remain private implementations.
// Execution lifecycle and operation qualification are separate contracts.
//
// The current HarnessConfig wire object is defined by proto.HarnessConfig.
// Omission and {} mean no native parameters. Explicit null, arrays, repeated
// object members and objects above MaxHarnessConfigBytes are rejected by the
// shared decoder in native.go. Errors must omit submitted keys and values.
// Native schemas must reject unknown fields and any setting that overrides model,
// provider/authentication, workspace, tools/MCP, permissions or lifecycle controls.
// Never merge arbitrary host configuration into the frozen model configuration.
//
// Acceptance obligates the adapter to apply every accepted parameter accurately
// through native configuration, SDK options or native Turn settings. Validate and
// prepare before native resources or model input; never ignore preparation errors,
// silently drop parameters or retry with weaker settings. Native resource cleanup
// and uncertain ownership follow the separate execution lifecycle contract.
//
// Core freezes the model configuration per Session. Its meaning and explicit
// settings must hold for the first Turn, later Turns, native retries, tool
// continuations and recovery. A Runtime that cannot preserve a frozen snapshot
// rejects it without rewriting, migration or aliases. Native connection setup is
// direct: no model API proxy, passthrough gateway or protocol conversion, including
// inside an adapter. Protocol acceptance does not qualify model capabilities;
// operation and input requirements must still be checked before native submission.
package harnessconfig

import (
	"errors"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Provider is deliberately limited to configuration compatibility. Core owns
// credential admission and endpoint security; native launch options stay private.
type Provider struct {
	Protocol            string
	RequiresTokenLimits bool
}

// Configuration is an adapter-owned declaration, not live provider readiness.
// Providers is ordered; its first entry is the default. An explicit empty
// declaration accepts no provider or nonempty native parameters. It is useful
// only for direct adapters whose model connection remains native-owned.
type Configuration struct {
	Providers []Provider
	// ValidateNativeConfig belongs to the selected adapter, never Core.
	ValidateNativeConfig func(proto.HarnessConfig) bool
}

// PreparedConfiguration owns a validated snapshot without native side effects.
// Model and Provider may be absent only for the existing native-owned connection
// path. A supplied model must be a nonempty string; an explicit provider requires
// an explicit model. HarnessConfig is always an independently owned object.
type PreparedConfiguration struct {
	Model         string
	Provider      *modelprovider.Provider
	HarnessConfig proto.HarnessConfig
}

var ErrModel = errors.New("model must be a nonempty model identifier")

// ValidateModel is shared by public admission and Runtime preparation.
func ValidateModel(value any) (string, error) {
	model, ok := value.(string)
	if !ok || strings.TrimSpace(model) == "" {
		return "", ErrModel
	}
	return model, nil
}

// Prepare validates the current model/provider/native-parameter contract before
// creating native resources. Other options belong to the execution contract.
func (c Configuration) Prepare(options map[string]any) (PreparedConfiguration, error) {
	var result PreparedConfiguration
	if value, present := options["model"]; present {
		model, err := ValidateModel(value)
		if err != nil {
			return PreparedConfiguration{}, err
		}
		result.Model = model
	}
	if value, present := options["model_provider"]; present {
		if result.Model == "" {
			return PreparedConfiguration{}, ErrModel
		}
		provider, err := c.ParseProvider(value)
		if err != nil {
			return PreparedConfiguration{}, err
		}
		result.Provider = &provider
	}
	native, err := c.PrepareHarnessConfig(options)
	if err != nil {
		return PreparedConfiguration{}, err
	}
	result.HarnessConfig = native
	return result, nil
}

// ValidateDeclaration rejects ambiguous support before registration. A caller
// must pass the declaration explicitly; there is no inferred built-in fallback.
func (c Configuration) ValidateDeclaration() error {
	seen := make(map[string]bool, len(c.Providers))
	for _, provider := range c.Providers {
		if !modelprovider.Protocol(provider.Protocol).Valid() || seen[provider.Protocol] {
			return errors.New("invalid harness configuration declaration")
		}
		seen[provider.Protocol] = true
	}
	return nil
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
		if kind == "" || configuration.ValidateDeclaration() != nil {
			panic("harnessconfig: invalid declaration")
		}
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
