// Package harnessconfig defines the shared Harness declaration. This file is
// the authoring entry point for a Harness: one Declaration states its kind,
// display name, model providers, native parameter validation, capabilities,
// placements and the public admission rules it adds. Core admission, support
// descriptions and every Runtime execution/preparation entry consume that same
// value, so nothing about a Harness is declared a second time elsewhere.
// Adapter-specific validation and native rendering remain private implementations.
// Execution lifecycle is a separate contract.
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
// inside an adapter. Protocol acceptance does not declare model capabilities;
// operation and input requirements must still be checked before native submission.
package harnessconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Provider is deliberately limited to configuration compatibility. Core owns
// credential admission and endpoint security; native launch options stay private.
type Provider struct {
	Protocol string
}

// Configuration is an adapter-owned declaration, not live provider readiness.
// Providers is ordered; its first entry is the default. An explicit empty
// declaration accepts no provider or nonempty native parameters. It is useful
// only for direct adapters whose model connection remains native-owned.
type Configuration struct {
	Providers []Provider
	// MaxOutputTokens reports that the adapter maps a provider's
	// max_output_tokens to a native setting. Every adapter maps context_window.
	MaxOutputTokens bool
	// ValidateNativeConfig belongs to the selected adapter, never Core.
	ValidateNativeConfig func(proto.HarnessConfig) bool
}

// Declaration is everything a Harness states about itself, authored once in
// internal/harnessconfig/<package>/declaration.go. The Runtime adapter binds it
// to native execution, and Core admission validates every Session against it:
// Core keeps no parallel per-Harness description.
//
// Every capability is Supported or Unsupported; the zero value is invalid.
// Runtime discovery may narrow Capabilities for the installed host (a missing
// binary, an unsupported platform) and never widens them. A nil validator means
// only the common validation applies.
type Declaration struct {
	// Kind is the public identifier; Label is its display name.
	Kind, Label string
	// Configuration declares model providers and native parameter validation.
	Configuration
	// Capabilities is the maximum the adapter supports on a capable host and the
	// operations Core may rely on once a connected Runtime advertises them.
	Capabilities proto.AgentKindCapabilities
	// Placements lists the Environment types the Harness executes in.
	Placements []string
	// MCPOrigins lists where HTTP MCP connections originate: "service" for the
	// service-side environment:none profile, "environment" for the Environment.
	MCPOrigins []string
	// Execution declares which official execution option values the Harness
	// maps to a native setting. Admission rejects any other user-set value.
	Execution ExecutionOptions
	// WhitespaceOnlyText declares messages without an image or non-whitespace
	// text. Unsupported Harnesses reject them at admission; Core never trims or
	// pads model input to fit a Harness.
	WhitespaceOnlyText proto.CapabilitySupport
	// ValidateConfiguration adds pure restrictions on a saved Agent and
	// Environment.
	ValidateConfiguration func(Admission) error
	// ValidateTools adds restrictions on resolved function and MCP tools.
	ValidateTools func([]proto.FunctionTool, []proto.MCPHTTPServer) error
	// ValidateMessageInput adds restrictions on messages that carry images.
	ValidateMessageInput func(proto.MessageInput) error
	// ValidateFunctionResult adds restrictions on submitted function results.
	ValidateFunctionResult func(proto.FunctionResultPayload) error
}

// Info is the Runtime descriptor before discovery has checked the host.
func (d Declaration) Info() proto.SupportedAgentKind {
	return proto.SupportedAgentKind{Kind: d.Kind, Capabilities: d.Capabilities}
}

// Accepts reports whether the Harness executes in the Environment type.
func (d Declaration) Accepts(placement string) bool {
	return slices.Contains(d.Placements, placement)
}

func (d Declaration) Clone() Declaration {
	d.Configuration = d.Configuration.Clone()
	d.Execution = d.Execution.Clone()
	d.Placements = slices.Clone(d.Placements)
	d.MCPOrigins = slices.Clone(d.MCPOrigins)
	return d
}

// ValidateDeclaration rejects ambiguous support before registration. Nothing is
// inferred: every capability, placement and origin is stated.
func (d Declaration) ValidateDeclaration() error {
	// The label is rendered into the generated Markdown catalog.
	if d.Kind == "" || d.Kind != strings.TrimSpace(d.Kind) || strings.TrimSpace(d.Label) == "" || strings.ContainsAny(d.Label, "\n\t|") {
		return errors.New("harnessconfig: invalid kind or label")
	}
	if err := d.Configuration.ValidateDeclaration(); err != nil {
		return err
	}
	if err := d.Capabilities.ValidateDeclaration(); err != nil {
		return err
	}
	if d.WhitespaceOnlyText != proto.CapabilitySupported && d.WhitespaceOnlyText != proto.CapabilityUnsupported {
		return errors.New("harnessconfig: whitespace-only text support must be declared")
	}
	if len(d.Placements) == 0 || !validChoices(d.Placements, "none", "openai_hosted", "self_hosted") {
		return errors.New("harnessconfig: invalid placements")
	}
	if d.MCPOrigins == nil || !validChoices(d.MCPOrigins, "service", "environment") ||
		(slices.Contains(d.MCPOrigins, "service") && !d.Accepts("none")) ||
		(slices.Contains(d.MCPOrigins, "environment") && !d.Accepts("openai_hosted") && !d.Accepts("self_hosted")) ||
		(d.Capabilities.MCPHTTPBearerAuth.IsSupported() && len(d.MCPOrigins) == 0) {
		return errors.New("harnessconfig: invalid MCP origins")
	}
	// Core always sends the official default text verbosity.
	if !slices.Contains(d.Execution.TextVerbosities, "medium") {
		return errors.New("harnessconfig: execution must declare text verbosity medium")
	}
	return nil
}

func validChoices(values []string, allowed ...string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !slices.Contains(allowed, value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
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

// Registry is an immutable snapshot. Missing declarations remain unknown rather
// than inheriting another harness's rules.
type Registry struct {
	declarations map[string]Declaration
}

// NewRegistry panics on an invalid or duplicate declaration. Static
// registrations are programmer errors, never runtime input.
func NewRegistry(declarations ...Declaration) Registry {
	registry := Registry{declarations: make(map[string]Declaration, len(declarations))}
	for _, declaration := range declarations {
		if _, duplicate := registry.declarations[declaration.Kind]; duplicate {
			panic("harnessconfig: duplicate declaration")
		}
		if err := declaration.ValidateDeclaration(); err != nil {
			panic(err)
		}
		registry.declarations[declaration.Kind] = declaration.Clone()
	}
	return registry
}

// IsZero reports a Registry that was never built by NewRegistry.
func (r Registry) IsZero() bool { return r.declarations == nil }

func (r Registry) Lookup(kind string) (Declaration, bool) {
	declaration, ok := r.declarations[kind]
	return declaration.Clone(), ok
}

// Kinds returns a stable snapshot of the declared Harness identifiers.
func (r Registry) Kinds() []string {
	kinds := slices.Collect(maps.Keys(r.declarations))
	slices.Sort(kinds)
	return kinds
}

func (r Registry) ValidateProtocol(kind, protocol string) error {
	declaration, _ := r.Lookup(kind)
	return declaration.ValidateProtocol(protocol)
}

func (r Registry) SupportsProtocol(protocol string) bool {
	for _, declaration := range r.declarations {
		if _, ok := declaration.Provider(protocol); ok {
			return true
		}
	}
	return false
}

// ErrInvalidInput marks a configuration a Harness declaration rejects. Core
// reports it as an invalid request, without echoing submitted values.
var ErrInvalidInput = errors.New("invalid harness configuration")

// Admission is the saved Agent and Environment a Harness declaration validates.
// Core projects its public types into it, so declarations stay independent of
// the public API package.
type Admission struct {
	TextFormat string
	TextSchema json.RawMessage
	MultiAgent bool
	Tools      []json.RawMessage
	// WorkspaceDirectory is the Environment's workspace, empty without one.
	WorkspaceDirectory string
}

// ValidateMCPOrigins preserves outbound authority independently of Harness names.
// Workspace connections never stand in for service-network connections.
func (d Declaration) ValidateMCPOrigins(placement string, servers []proto.MCPHTTPServer) error {
	for _, server := range servers {
		switch server.ConnectionOrigin {
		case "service":
			if placement != "none" {
				return errors.New("Service-origin MCP requires the service-side environment:none profile.")
			}
		case "environment":
			if placement != "openai_hosted" && placement != "self_hosted" {
				return errors.New("Environment-origin MCP requires a managed or self-hosted execution Environment.")
			}
		default:
			return errors.New("MCP requires an explicit connection origin.")
		}
		if !slices.Contains(d.MCPOrigins, server.ConnectionOrigin) {
			return errors.New("The configured engine does not support this MCP connection origin.")
		}
	}
	return nil
}

// RequireObjectSchemas rejects a function whose parameter schema has no root type object.
func RequireObjectSchemas(functions []proto.FunctionTool) error {
	for _, tool := range functions {
		var schema struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(tool.Parameters, &schema) != nil || schema.Type != "object" {
			return errors.New("The configured engine requires function schemas with root type object.")
		}
	}
	return nil
}

// ExecutionOptions lists the official execution option values a Harness maps to
// a native setting. An empty set declares the option unsupported. Absent
// options need no declaration: they follow the Harness's native default. Auto
// service tier means unset, so it is always accepted.
type ExecutionOptions struct {
	// WebSearchModes are the supported web_search modes: disabled, cached, live.
	WebSearchModes []string
	// WebSearchSettings reports native homes for context_size, allowed_domains
	// and location.
	WebSearchSettings bool
	// ReasoningEfforts and ReasoningSummaries are the supported reasoning values.
	ReasoningEfforts, ReasoningSummaries []string
	// ServiceTiers are the supported tiers other than auto.
	ServiceTiers []string
	// ProgrammaticToolCalling lists the supported enabled values.
	ProgrammaticToolCalling []bool
	// TextVerbosities are the supported text verbosity levels.
	TextVerbosities []string
}

func (o ExecutionOptions) Clone() ExecutionOptions {
	o.WebSearchModes = slices.Clone(o.WebSearchModes)
	o.ReasoningEfforts = slices.Clone(o.ReasoningEfforts)
	o.ReasoningSummaries = slices.Clone(o.ReasoningSummaries)
	o.ServiceTiers = slices.Clone(o.ServiceTiers)
	o.ProgrammaticToolCalling = slices.Clone(o.ProgrammaticToolCalling)
	o.TextVerbosities = slices.Clone(o.TextVerbosities)
	return o
}

// ValidateExecutionControls rejects a user-set or default value the Harness
// does not declare. Core admission and the Runtime call it with the same
// controls, so neither keeps a second list. Messages name the field and never
// echo configuration other than the rejected value.
func (d Declaration) ValidateExecutionControls(controls *proto.ExecutionControls) error {
	if controls == nil {
		return nil
	}
	o := d.Execution
	reject := func(field, value string) error {
		return fmt.Errorf("%s %q is not supported by this Harness.", field, value)
	}
	if search := controls.WebSearch; search != nil {
		if !slices.Contains(o.WebSearchModes, search.Mode) {
			return reject("web_search.mode", search.Mode)
		}
		// Settings of disabled search configure nothing.
		if search.Mode != "disabled" && !o.WebSearchSettings && (search.ContextSize != "medium" || search.AllowedDomains != nil || search.Location != nil) {
			return errors.New("web_search context_size, allowed_domains and location are not supported by this Harness.")
		}
	}
	if enabled := controls.ProgrammaticToolCalling; enabled != nil && !slices.Contains(o.ProgrammaticToolCalling, *enabled) {
		return fmt.Errorf("programmatic_tool_calling.enabled=%t is not supported by this Harness.", *enabled)
	}
	if v := controls.ReasoningEffort; v != "" && !slices.Contains(o.ReasoningEfforts, v) {
		return reject("reasoning.effort", v)
	}
	if v := controls.ReasoningSummary; v != "" && !slices.Contains(o.ReasoningSummaries, v) {
		return reject("reasoning.summary", v)
	}
	if v := controls.ServiceTier; v != "" && !slices.Contains(o.ServiceTiers, v) {
		return reject("service_tier", v)
	}
	if v := controls.TextVerbosity; v != "" && !slices.Contains(o.TextVerbosities, v) {
		return reject("text.verbosity", v)
	}
	return nil
}
