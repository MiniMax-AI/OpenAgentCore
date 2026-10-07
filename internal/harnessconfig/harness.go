// Package harnessconfig defines the shared model configuration contract.
// This file is the authoring entry point for Harness configuration: declare
// native provider support, parameter validation and the Harness's support
// Declaration once, then bind that same Configuration when registering the
// Runtime Harness. Core admission, support descriptions and every Runtime
// execution/preparation entry consume it.
// Adapter-specific validation and native rendering remain private implementations.
// Execution lifecycle and operation qualification are separate contracts.
//
// The request's typed model configuration is defined by proto.PromptRequestPayload,
// and its HarnessConfig wire object by proto.HarnessConfig. Omission and {} mean
// no native parameters. Explicit null, arrays, repeated object members and
// objects above MaxHarnessConfigBytes are rejected by the shared decoder in
// native.go. Errors must omit submitted keys and values.
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
// rejects it without rewriting, migration or aliases. Native connection setup
// goes through the credential gateway that internal/modelprovider declares, with
// no protocol conversion, including inside an adapter. Protocol acceptance does
// not qualify model capabilities; operation and input requirements must still be
// checked before native submission.
package harnessconfig

import (
	"errors"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Provider is deliberately limited to configuration compatibility.
// internal/modelprovider owns the credential and endpoint rule; native launch
// options stay private.
type Provider struct {
	Protocol            string
	RequiresTokenLimits bool
}

// Configuration is an adapter-owned declaration, not live provider readiness.
// Providers is ordered; its first entry is the default.
type Configuration struct {
	Providers []Provider
	// ValidateNativeConfig belongs to the selected adapter, never Core. It
	// receives the decoded harness_config object.
	ValidateNativeConfig func(map[string]any) bool
	// Declaration is the only authored source of the Harness's support. A
	// Runtime's heartbeat only narrows its Capabilities.
	Declaration proto.Declaration
}

// PreparedConfiguration owns a validated snapshot without native side effects:
// a nonempty model, the provider and an independently owned HarnessConfig.
type PreparedConfiguration struct {
	Model         string
	Provider      modelprovider.Provider
	HarnessConfig map[string]any
}

var (
	ErrModel         = errors.New("model must be a nonempty model identifier")
	ErrModelProvider = errors.New("a model provider is required")
)

// ValidateModel is shared by public admission and Runtime preparation.
func ValidateModel(value any) (string, error) {
	model, ok := value.(string)
	if !ok || strings.TrimSpace(model) == "" {
		return "", ErrModel
	}
	return model, nil
}

// Prepare validates the request's model, model provider and native parameters
// against this declaration before creating native resources. Every request
// names a model and a provider; the provider must be one this declaration
// supports, with the token limits it requires.
func (c Configuration) Prepare(req proto.PromptRequestPayload) (PreparedConfiguration, error) {
	model, err := ValidateModel(req.Model)
	if err != nil {
		return PreparedConfiguration{}, err
	}
	if req.ModelProvider == nil {
		return PreparedConfiguration{}, ErrModelProvider
	}
	provider := *req.ModelProvider
	// A view hands its Harness the credential gateway's loopback http listener.
	if err := provider.Validate(true); err != nil {
		return PreparedConfiguration{}, err
	}
	if err := c.Validate(string(provider.Protocol), provider.ContextWindow, provider.MaxOutputTokens); err != nil {
		return PreparedConfiguration{}, err
	}
	native, err := c.ParseHarnessConfig(req.HarnessConfig)
	if err != nil {
		return PreparedConfiguration{}, err
	}
	return PreparedConfiguration{Model: model, Provider: provider, HarnessConfig: native}, nil
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
	return c.Declaration.ValidateDeclaration()
}

func (c Configuration) Clone() Configuration {
	c.Providers = slices.Clone(c.Providers)
	c.Declaration.MCPOrigins = slices.Clone(c.Declaration.MCPOrigins)
	c.Declaration.ReservedMCPLabels = slices.Clone(c.Declaration.ReservedMCPLabels)
	c.Declaration.Conflicts = slices.Clone(c.Declaration.Conflicts)
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
