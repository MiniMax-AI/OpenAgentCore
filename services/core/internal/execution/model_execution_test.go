package execution

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSessionModelExecutionNeverFallsBack(t *testing.T) {
	called := false
	d := Dispatcher{Options: func(context.Context, store.Session) (map[string]any, error) {
		called = true
		return map[string]any{"model_provider": map[string]any{"base_url": "http://127.0.0.1:1/v1"}}, nil
	}}
	if _, err := d.executionRequest(t.Context(), store.Session{Engine: "codex"}, Snapshot{ModelProviderConfigured: true}, device.KindCapabilities{}, store.SessionExecutionBinding{}); err == nil || called {
		t.Fatal("missing Session credentials fell back")
	}
	// Hosted and self-hosted Runtimes have no model configuration of their own.
	for _, environment := range []string{"openai_hosted", "self_hosted"} {
		snapshot := Snapshot{Environment: &v1.Environment{Type: environment}}
		if _, err := d.executionRequest(t.Context(), store.Session{Engine: "codex"}, snapshot, device.KindCapabilities{}, store.SessionExecutionBinding{}); !errors.Is(err, store.ErrModelProviderRequired) || called {
			t.Fatal("provider-free Session dispatched", environment, err)
		}
	}
	// A none device may supply its own provider environment.
	request, err := d.executionRequest(t.Context(), store.Session{Engine: "codex"}, Snapshot{Environment: &v1.Environment{Type: "none"}}, device.KindCapabilities{}, store.SessionExecutionBinding{})
	if err != nil || !called || request.AgentOptions["model_provider"] == nil {
		t.Fatal("none Session lost its adapter options", err)
	}
}

func TestSessionModelOptionsPreserveUpstreamBundleForEveryHarness(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk", "mcode"} {
		for _, protocol := range []string{"anthropic", "responses", "chat_completions"} {
			t.Run(engine+"/"+protocol, func(t *testing.T) {
				provider := &v1.ModelProviderInput{Protocol: protocol, BaseURL: "https://example.com/v1", APIKey: "private-key", ContextWindow: 200000, MaxOutputTokens: 8000}
				got, err := resolvedSessionModelOptions(provider, engine)
				native := engine == "mcode" || engine == "codex" && protocol == "responses" || engine == "claude_sdk" && protocol == "anthropic"
				if !native {
					var protocolError *v1.ModelProviderError
					if got != nil || !errors.As(err, &protocolError) || strings.Contains(err.Error(), provider.APIKey) {
						t.Fatal("non-native provider was not safely rejected")
					}
					if provider.Protocol != protocol {
						t.Fatal("rejection rewrote the frozen provider protocol")
					}
					return
				}
				want := map[string]any{"model_provider": map[string]any{
					"protocol": protocol, "base_url": provider.BaseURL, "api_key": provider.APIKey,
					"context_window": provider.ContextWindow, "max_output_tokens": provider.MaxOutputTokens,
				}}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("upstream provider bundle was changed", err)
				}
			})
		}
	}
}

func TestSessionModelOptionsValidateAdmission(t *testing.T) {
	provider := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.com/v1", APIKey: "private-key"}
	for _, engine := range []string{"mcode", "unknown", ""} {
		if _, err := resolvedSessionModelOptions(provider, engine); err == nil {
			t.Fatalf("invalid provider/harness configuration accepted for %q", engine)
		}
	}
	if _, err := resolvedSessionModelOptions(nil, "codex"); err == nil {
		t.Fatal("missing provider accepted")
	}
	provider.Protocol = "chat-completions"
	if _, err := resolvedSessionModelOptions(provider, "codex"); err == nil {
		t.Fatal("protocol alias accepted")
	}
}
