package v1

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

// ModelConfigurationInput combines the existing provider contract with a model
// and adapter-owned native model parameters. It is never a model catalog entry.
type ModelConfigurationInput struct {
	ModelProvider ModelProviderInput `json:"model_provider" binding:"required"`
	Model         string             `json:"model" binding:"required"`
	HarnessConfig json.RawMessage    `json:"harness_config,omitempty" swaggertype:"object"`
}

type ModelConfigurationView struct {
	ModelProvider *ModelProviderView `json:"model_provider" binding:"required"`
	Model         string             `json:"model" binding:"required"`
	HarnessConfig json.RawMessage    `json:"harness_config" swaggertype:"object" binding:"required"`
}

func (c ModelConfigurationInput) ValidateHarness(harness string) error {
	if err := c.ModelProvider.ValidateConfiguration(harness, c.HarnessConfig); err != nil {
		return err
	}
	return ValidateNativeModelConfiguration(harness, c.Model, c.HarnessConfig)
}

func ValidateHarnessConfig(harness string, raw json.RawMessage) error {
	if err := builtin.Registry().ValidateHarnessConfig(harness, raw); err != nil {
		return &ModelProviderError{Code: "harness_config_invalid", Param: "harness_config", message: "harness_config contains unsupported or invalid native model parameters"}
	}
	return nil
}

func (c ModelConfigurationInput) SafeView() ModelConfigurationView {
	return ModelConfigurationView{ModelProvider: c.ModelProvider.SafeView(), Model: c.Model, HarnessConfig: ResolvedHarnessConfig(c.HarnessConfig)}
}

func ResolvedHarnessConfig(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), raw...)
}

// ValidateNativeModelConfiguration checks the resolved public execution inputs
// through the same adapter validation used by deployment administration.
func ValidateNativeModelConfiguration(harness, model string, raw json.RawMessage) error {
	if _, err := harnessconfig.ValidateModel(model); err != nil {
		return &ModelProviderError{Code: "model_configuration_model_invalid", Param: "model", message: "model must be a nonempty model identifier"}
	}
	return ValidateHarnessConfig(harness, raw)
}

// ModelConfigurationSupport describes this build, not remote-model availability.
type ModelConfigurationSupport struct {
	Protocols            []string `json:"protocols" binding:"required"`
	AcceptsHarnessConfig bool     `json:"accepts_harness_config" binding:"required"`
	TokenLimitsRequired  bool     `json:"token_limits_required" binding:"required"`
}

func (p *ModelProviderInput) ValidateConfiguration(harness string, native json.RawMessage) error {
	if err := p.ValidateHarness(harness); err != nil {
		return err
	}
	return ValidateHarnessConfig(harness, native)
}
