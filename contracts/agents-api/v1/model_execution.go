package v1

import (
	"encoding/json"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

// ModelProviderError preserves the shared validation message while allowing Core
// administration to identify a field. It contains no submitted values.
type ModelProviderError struct {
	Code, Param string
	message     string
}

func (e *ModelProviderError) Error() string { return e.message }

// SessionExecutionInput is a write-only execution extension, not a provider resource.
type SessionExecutionInput struct {
	// Environment supplies placement-independent preparation through the Core extension.
	Environment   json.RawMessage     `json:"environment,omitempty" swaggertype:"object"`
	ModelProvider *ModelProviderInput `json:"model_provider,omitempty"`
	HarnessConfig json.RawMessage     `json:"harness_config,omitempty" swaggertype:"object"`
}

type ModelProviderInput struct {
	Protocol        string `json:"protocol" enums:"anthropic,responses,chat_completions" binding:"required"`
	BaseURL         string `json:"base_url" binding:"required"`
	APIKey          string `json:"api_key" binding:"required"`
	ContextWindow   int32  `json:"context_window,omitempty"`
	MaxOutputTokens int32  `json:"max_output_tokens,omitempty"`
}

func (p *ModelProviderInput) Validate() error {
	return p.validate(builtin.Registry())
}

func (p *ModelProviderInput) validate(registry harnessconfig.Registry) error {
	if p == nil {
		return &ModelProviderError{Code: "invalid_model_provider", Param: "", message: "model_provider is required"}
	}
	if !validModelProviderBaseURL(p.BaseURL) {
		return &ModelProviderError{Code: "model_provider_base_url_invalid", Param: "base_url", message: "model provider requires an HTTPS base_url without credentials, query or fragment"}
	}
	if !registry.SupportsProtocol(p.Protocol) {
		return &ModelProviderError{Code: "model_provider_protocol_unsupported", Param: "protocol", message: "unsupported model provider protocol"}
	}
	if strings.TrimSpace(p.APIKey) == "" || len(p.APIKey) > 16384 || strings.ContainsAny(p.APIKey, "\x00\r\n") {
		return &ModelProviderError{Code: "model_provider_api_key_invalid", Param: "api_key", message: "invalid model provider API key"}
	}
	if p.ContextWindow < 0 || p.MaxOutputTokens < 0 || (p.MaxOutputTokens > p.ContextWindow) {
		param := "max_output_tokens"
		if p.ContextWindow < 0 {
			param = "context_window"
		}
		return &ModelProviderError{Code: "model_provider_token_limits_invalid", Param: param, message: "invalid model token limits"}
	}
	return nil
}

func (p *ModelProviderInput) ValidateHarness(harness string) error {
	return p.ValidateHarnessWithRegistry(harness, builtin.Registry())
}

// ValidateHarnessWithRegistry validates provider input against adapter-owned rules.
// It does not enable an execution engine or placement.
func (p *ModelProviderInput) ValidateHarnessWithRegistry(harness string, registry harnessconfig.Registry) error {
	if err := p.validate(registry); err != nil {
		return err
	}
	configuration, _ := registry.Lookup(harness)
	if err := configuration.ValidateProtocol(p.Protocol); err != nil {
		return &ModelProviderError{Code: "model_provider_protocol_unsupported", Param: "protocol", message: err.Error()}
	}
	if err := configuration.Validate(p.Protocol, p.ContextWindow, p.MaxOutputTokens); err != nil {
		param := "max_output_tokens"
		if p.ContextWindow <= 0 {
			param = "context_window"
		}
		return &ModelProviderError{Code: "model_provider_token_limits_invalid", Param: param, message: err.Error()}
	}
	return nil
}

func ValidateModelProtocol(protocol, harness string) error {
	return builtin.Registry().ValidateProtocol(harness, protocol)
}
