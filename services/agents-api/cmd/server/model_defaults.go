package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Adapt the existing operator configuration only at the server composition
// boundary. Core admission and scheduling consume one provider bundle.
func deploymentModelDefaults(options func(context.Context, store.Session) (map[string]any, error)) api.ModelProviderDefaults {
	if options == nil {
		return nil
	}
	return func(ctx context.Context, harness, model string) (*v1.ModelProviderInput, map[string]any, error) {
		native, err := options(ctx, store.Session{Engine: harness})
		if err != nil {
			return nil, nil, err
		}
		key := map[string]string{"codex": "codex_provider", "claude_sdk": "claude_provider", "mcode": "mcode_provider"}[harness]
		raw, exists := native[key]
		if !exists {
			return nil, nil, nil
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, nil, errors.New("invalid deployment model provider")
		}
		provider := &v1.ModelProviderInput{}
		if harness == "mcode" {
			var cfg struct {
				NPM     *string `json:"npm"`
				Enabled *bool   `json:"enabled"`
				Options struct {
					BaseURL string `json:"baseURL"`
					APIKey  string `json:"apiKey"`
				} `json:"options"`
				Models map[string]struct {
					Limit struct {
						Context int32 `json:"context"`
						Output  int32 `json:"output"`
					} `json:"limit"`
				} `json:"models"`
			}
			if json.Unmarshal(encoded, &cfg) != nil {
				return nil, nil, errors.New("invalid deployment model provider")
			}
			if (cfg.NPM != nil && *cfg.NPM != "@ai-sdk/anthropic") || (cfg.Enabled != nil && !*cfg.Enabled) {
				return nil, nil, errors.New("unsupported deployment model provider")
			}
			provider.Protocol, provider.BaseURL, provider.APIKey = "anthropic", cfg.Options.BaseURL, cfg.Options.APIKey
			limits, exists := cfg.Models[model]
			if !exists {
				return nil, nil, errors.New("deployment model limits are unavailable")
			}
			provider.ContextWindow, provider.MaxOutputTokens = limits.Limit.Context, limits.Limit.Output
		} else {
			var cfg struct {
				BaseURL     string `json:"base_url"`
				BearerToken string `json:"bearer_token"`
				WireAPI     string `json:"wire_api"`
			}
			if json.Unmarshal(encoded, &cfg) != nil {
				return nil, nil, errors.New("invalid deployment model provider")
			}
			provider.Protocol, provider.BaseURL, provider.APIKey = "anthropic", cfg.BaseURL, cfg.BearerToken
			if harness == "codex" {
				provider.Protocol = "responses"
				if wireAPI := strings.TrimSpace(cfg.WireAPI); wireAPI != "" && wireAPI != "responses" {
					return nil, nil, errors.New("unsupported deployment model protocol")
				}
			}
		}
		if err := provider.ValidateHarness(harness); err != nil {
			return nil, nil, err
		}
		return provider, native, nil
	}
}
