package execution

import (
	"context"
	"errors"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"reflect"
	"testing"
)

func TestSessionModelExecutionNeverFallsBack(t *testing.T) {
	called := false
	d := Dispatcher{Options: func(context.Context, store.Session) (map[string]any, error) {
		called = true
		return map[string]any{"codex_provider": map[string]any{"base_url": "http://127.0.0.1:1/v1"}}, nil
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
	if err != nil || !called || request.AgentOptions["codex_provider"] == nil {
		t.Fatal("none Session lost its adapter options", err)
	}
}

func TestFrozenDeploymentOptionsRetainNativeProviderSettings(t *testing.T) {
	provider := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.com/v1", APIKey: "private-key"}
	frozen := map[string]any{"codex_provider": map[string]any{
		"base_url": provider.BaseURL, "bearer_token": provider.APIKey, "wire_api": "responses",
		"http_headers": map[string]any{"x-custom-header": "private-header"},
		"query_params": map[string]any{"api-version": "2026-01-01"},
	}, "mode": "default"}
	got, err := resolvedSessionModelOptions(provider, frozen, "codex", "actual-model")
	if err != nil || !reflect.DeepEqual(got, frozen) {
		t.Fatal("native deployment settings were lost", err)
	}
	if _, err := resolvedSessionModelOptions(provider, frozen, "claude_sdk", "actual-model"); err == nil {
		t.Fatal("frozen options bypassed provider/harness validation")
	}
	explicit, err := resolvedSessionModelOptions(provider, nil, "codex", "actual-model")
	if err != nil {
		t.Fatal(err)
	}
	codex, ok := explicit["codex_provider"].(map[string]any)
	if !ok || codex["base_url"] != provider.BaseURL || codex["bearer_token"] != provider.APIKey || len(codex) != 3 {
		t.Fatal("explicit provider mapping changed")
	}
}
