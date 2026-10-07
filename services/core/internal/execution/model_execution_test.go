package execution

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestSessionModelExecutionNeverFallsBack(t *testing.T) {
	reader, _ := testSessions(t, pgtest.Open(t), nil)
	d := Dispatcher{SessionsReader: reader}
	session := sessions.Session{TenantID: uuid.NewString(), ID: uuid.NewString(), Engine: "codex"}
	if _, err := d.executionRequest(t.Context(), session, Snapshot{ModelProviderConfigured: true}, runtimedevice.KindCapabilities{}, sessions.ExecutionBinding{}); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("missing Session credentials fell back", err)
	}
	// Hosted and self-hosted Runtimes have no model configuration of their own.
	for _, environment := range []string{"openai_hosted", "self_hosted"} {
		snapshot := Snapshot{Environment: &v1.Environment{Type: environment}}
		if _, err := d.executionRequest(t.Context(), sessions.Session{Engine: "codex"}, snapshot, runtimedevice.KindCapabilities{}, sessions.ExecutionBinding{}); !errors.Is(err, ErrModelProviderRequired) {
			t.Fatal("provider-free Session dispatched", environment, err)
		}
	}
	// A none device without a frozen provider uses its own provider environment:
	// Core sends only the Agent's model and instructions.
	instructions := "Keep this instruction."
	snapshot := Snapshot{Agent: v1.Agent{Model: "device-model", Instructions: &instructions}, Environment: &v1.Environment{Type: "none"}}
	request, err := d.executionRequest(t.Context(), sessions.Session{Engine: "codex"}, snapshot, runtimedevice.KindCapabilities{}, sessions.ExecutionBinding{})
	if err != nil || request.Model != "device-model" || request.SystemPrompt != instructions || request.ModelProvider != nil || request.HarnessConfig != nil {
		t.Fatal("none Session received model settings Core does not own", err)
	}
}

func TestSessionModelProviderPreservesUpstreamBundleForEveryHarness(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk", "mcode"} {
		for _, protocol := range []string{"anthropic", "responses", "chat_completions"} {
			t.Run(engine+"/"+protocol, func(t *testing.T) {
				provider := &v1.ModelProviderInput{Protocol: protocol, BaseURL: "https://example.com", APIKey: "private-key", ContextWindow: 200000, MaxOutputTokens: 8000}
				got, err := resolvedSessionModelProvider(provider, engine)
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
				want := modelprovider.Provider{Protocol: modelprovider.Protocol(protocol), BaseURL: provider.BaseURL, APIKey: provider.APIKey,
					ContextWindow: provider.ContextWindow, MaxOutputTokens: provider.MaxOutputTokens}
				if err != nil || got == nil || *got != want {
					t.Fatal("upstream provider bundle was changed", err)
				}
			})
		}
	}
}

func TestSessionModelProviderValidatesAdmission(t *testing.T) {
	provider := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.com/v1", APIKey: "private-key"}
	for _, engine := range []string{"mcode", "unknown", ""} {
		if _, err := resolvedSessionModelProvider(provider, engine); err == nil {
			t.Fatalf("invalid provider/harness configuration accepted for %q", engine)
		}
	}
	if _, err := resolvedSessionModelProvider(nil, "codex"); err == nil {
		t.Fatal("missing provider accepted")
	}
	provider.Protocol = "chat-completions"
	if _, err := resolvedSessionModelProvider(provider, "codex"); err == nil {
		t.Fatal("protocol alias accepted")
	}
}
