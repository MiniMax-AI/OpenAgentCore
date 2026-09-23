package v1

import (
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig/builtin"
)

// SessionExecutionInput is a write-only execution extension, not a provider resource.
type SessionExecutionInput struct {
	ModelProvider *ModelProviderInput `json:"model_provider,omitempty"`
	SandboxNodeID *string             `json:"sandbox_node_id,omitempty"`
}

type ModelProviderInput struct {
	Protocol        string `json:"protocol" enums:"anthropic,responses" binding:"required"`
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
		return errors.New("model_provider is required")
	}
	if !validModelProviderBaseURL(p.BaseURL) {
		return errors.New("model provider requires an HTTPS base_url without credentials, query or fragment")
	}
	if !registry.SupportsProtocol(p.Protocol) {
		return errors.New("unsupported model provider protocol")
	}
	if strings.TrimSpace(p.APIKey) == "" || len(p.APIKey) > 16384 || strings.ContainsAny(p.APIKey, "\x00\r\n") {
		return errors.New("invalid model provider API key")
	}
	if p.ContextWindow < 0 || p.MaxOutputTokens < 0 || (p.MaxOutputTokens > p.ContextWindow) {
		return errors.New("invalid model token limits")
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
	return p.SafeView().ValidateHarnessWithRegistry(harness, registry)
}

func ValidateModelProtocol(protocol, harness string) error {
	return builtin.Registry().ValidateProtocol(harness, protocol)
}
