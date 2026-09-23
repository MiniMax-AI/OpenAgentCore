package v1

import "errors"

// SavedAgentCoreInput carries defaults for future Sessions. The provider bundle
// is replaced as a whole; its API key is write-only.
type SavedAgentCoreInput struct {
	Harness       string              `json:"harness,omitempty" enums:"codex,claude_sdk,mcode"`
	ModelProvider *ModelProviderInput `json:"model_provider,omitempty" extensions:"x-nullable"`
}

// SavedAgentCore is the non-confidential representation of saved defaults.
type SavedAgentCore struct {
	Harness       string             `json:"harness,omitempty" enums:"codex,claude_sdk,mcode"`
	ModelProvider *ModelProviderView `json:"model_provider,omitempty"`
}

type ModelProviderView struct {
	Protocol         string `json:"protocol" enums:"anthropic,responses" binding:"required"`
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
	if x.ModelProvider == nil {
		return nil
	}
	if x.Harness != "" {
		return x.ModelProvider.ValidateHarness(x.Harness)
	}
	return x.ModelProvider.Validate()
}

func (x *SavedAgentCoreInput) SafeView() *SavedAgentCore {
	if x == nil {
		return nil
	}
	return &SavedAgentCore{Harness: x.Harness, ModelProvider: x.ModelProvider.SafeView()}
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
	if p == nil {
		return errors.New("model_provider is required")
	}
	if err := ValidateModelProtocol(p.Protocol, harness); err != nil {
		return err
	}
	if harness == "mcode" && (p.ContextWindow == 0 || p.MaxOutputTokens == 0) {
		return errors.New("MiniMax Code requires model context_window and max_output_tokens")
	}
	return nil
}
