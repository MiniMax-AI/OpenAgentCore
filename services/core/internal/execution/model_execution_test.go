package execution

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSessionModelExecutionNeverFallsBack(t *testing.T) {
	var d Dispatcher
	if _, err := d.executionRequest(t.Context(), store.Session{Engine: "codex"}, Snapshot{ModelProviderConfigured: true}, runtimedevice.KindCapabilities{}, store.SessionExecutionBinding{}); err == nil {
		t.Fatal("missing Session credentials fell back")
	}
	// Hosted and self-hosted Runtimes have no model configuration of their own.
	for _, environment := range []string{"openai_hosted", "self_hosted"} {
		snapshot := Snapshot{Environment: &v1.Environment{Type: environment}}
		if _, err := d.executionRequest(t.Context(), store.Session{Engine: "codex"}, snapshot, runtimedevice.KindCapabilities{}, store.SessionExecutionBinding{}); !errors.Is(err, ErrModelProviderRequired) {
			t.Fatal("provider-free Session dispatched", environment, err)
		}
	}
	// A none device without a frozen provider uses its own provider environment:
	// Core sends only the Agent's model and instructions.
	instructions := "Keep this instruction."
	snapshot := Snapshot{Agent: v1.Agent{Model: "device-model", Instructions: &instructions}, Environment: &v1.Environment{Type: "none"}}
	request, err := d.executionRequest(t.Context(), store.Session{Engine: "codex"}, snapshot, runtimedevice.KindCapabilities{}, store.SessionExecutionBinding{})
	if err != nil || !reflect.DeepEqual(request.AgentOptions, map[string]any{"model": "device-model", "system_prompt": &instructions}) {
		t.Fatal("none Session received adapter options Core does not own", request.AgentOptions, err)
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
