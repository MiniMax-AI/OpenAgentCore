package v1

import (
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// ModelProviderError preserves the shared validation message while allowing Core
// administration to identify a field. It contains no submitted values.
type ModelProviderError struct {
	Code, Param string
	message     string
}

func (e *ModelProviderError) Error() string { return e.message }

// modelProviderErrorCodes maps each modelprovider.FieldError field to its code.
var modelProviderErrorCodes = map[string]string{
	"base_url":          "model_provider_base_url_invalid",
	"protocol":          "model_provider_protocol_unsupported",
	"api_key":           "model_provider_api_key_invalid",
	"context_window":    "model_provider_token_limits_invalid",
	"max_output_tokens": "model_provider_token_limits_invalid",
}

// Model provider sources, as recorded in a Session's execution configuration.
const (
	ModelProviderSourceSession    = "session"
	ModelProviderSourceAgent      = "agent"
	ModelProviderSourceDeployment = "deployment"
)

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

// Provider is the bundle as the Harness–Model provider protocol carries it.
func (p *ModelProviderInput) Provider() modelprovider.Provider {
	return modelprovider.Provider{Protocol: modelprovider.Protocol(p.Protocol), BaseURL: p.BaseURL, APIKey: p.APIKey,
		ContextWindow: p.ContextWindow, MaxOutputTokens: p.MaxOutputTokens}
}

// Validate applies modelprovider's rule, without loopback http, and reports
// the rejected field with its public code.
func (p *ModelProviderInput) Validate() error {
	if p == nil {
		return &ModelProviderError{Code: "invalid_model_provider", message: "model_provider is required"}
	}
	err := p.Provider().Validate(false)
	var field *modelprovider.FieldError
	if !errors.As(err, &field) {
		return err
	}
	return &ModelProviderError{Code: modelProviderErrorCodes[field.Field], Param: field.Field, message: field.Error()}
}

// ValidateHarness also validates provider input against adapter-owned rules.
// It does not enable an execution engine or placement.
func (p *ModelProviderInput) ValidateHarness(harness string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	configuration, _ := builtin.Registry().Lookup(harness)
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
