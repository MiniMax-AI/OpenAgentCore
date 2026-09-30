package v1

import (
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

// SavedAgentCoreInput carries defaults for future Sessions. The provider bundle
// is replaced as a whole; its API key is write-only.
type SavedAgentCoreInput struct {
	HarnessConfig json.RawMessage     `json:"harness_config,omitempty" swaggertype:"object"`
	Harness       string              `json:"harness,omitempty"`
	ModelProvider *ModelProviderInput `json:"model_provider,omitempty" extensions:"x-nullable"`
}

// SavedAgentCore is the non-confidential representation of saved defaults.
type SavedAgentCore struct {
	HarnessConfig json.RawMessage    `json:"harness_config,omitempty" swaggertype:"object"`
	Harness       string             `json:"harness,omitempty"`
	ModelProvider *ModelProviderView `json:"model_provider,omitempty"`
}

type ModelProviderView struct {
	Protocol         string `json:"protocol" enums:"anthropic,responses,chat_completions" binding:"required"`
	BaseURL          string `json:"base_url" binding:"required"`
	ContextWindow    int32  `json:"context_window,omitempty"`
	MaxOutputTokens  int32  `json:"max_output_tokens,omitempty"`
	APIKeyConfigured bool   `json:"api_key_configured" binding:"required"`
}

func (x *SavedAgentCoreInput) Validate() error {
	if x == nil {
		return nil
	}
	if x.Harness != "" {
		if err := (&AgentsCore{Harness: x.Harness}).Validate(); err != nil {
			return err
		}
	}
	if err := ValidateHarnessConfig(x.Harness, x.HarnessConfig); err != nil {
		return err
	}
	if x.ModelProvider == nil {
		return nil
	}
	if x.Harness != "" {
		return x.ModelProvider.ValidateConfiguration(x.Harness, x.HarnessConfig)
	}
	return x.ModelProvider.Validate()
}

func (x *SavedAgentCoreInput) SafeView() *SavedAgentCore {
	if x == nil {
		return nil
	}
	return &SavedAgentCore{Harness: x.Harness, ModelProvider: x.ModelProvider.SafeView(), HarnessConfig: append(json.RawMessage(nil), x.HarnessConfig...)}
}

func (p *ModelProviderInput) SafeView() *ModelProviderView {
	if p == nil {
		return nil
	}
	return &ModelProviderView{
		Protocol: p.Protocol, BaseURL: p.BaseURL, ContextWindow: p.ContextWindow,
		MaxOutputTokens: p.MaxOutputTokens, APIKeyConfigured: p.APIKey != "",
	}
}

// ValidateHarness checks non-confidential protocol and limit compatibility.
func (p *ModelProviderView) ValidateHarness(harness string) error {
	return p.ValidateHarnessWithRegistry(harness, builtin.Registry())
}

func (p *ModelProviderView) ValidateHarnessWithRegistry(harness string, registry harnessconfig.Registry) error {
	if p == nil {
		return errors.New("model_provider is required")
	}
	return registry.Validate(harness, p.Protocol, p.ContextWindow, p.MaxOutputTokens)
}
