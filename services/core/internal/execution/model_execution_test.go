package execution

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// frozenProvider reads, for every Session, the bundle a Session of engine
// froze: Responses for Codex, Anthropic otherwise.
type frozenProvider struct {
	sessions.Reader
	engine string
}

func (r frozenProvider) SessionModelExecution(context.Context, string, string) (*v1.ModelProviderInput, error) {
	if r.engine == "codex" {
		return &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.example/v1", APIKey: "key"}, nil
	}
	return &v1.ModelProviderInput{Protocol: "anthropic", BaseURL: "https://model.example", APIKey: "key"}, nil
}

func TestSessionModelExecutionNeverFallsBack(t *testing.T) {
	reader, _ := testSessions(t, pgtest.Open(t), nil)
	d := Dispatcher{SessionsReader: reader}
	session := sessions.Session{TenantID: uuid.NewString(), ID: uuid.NewString(), Engine: "codex"}
	if _, err := d.executionRequest(t.Context(), session, Snapshot{}, proto.Declaration{}, sessions.ExecutionBinding{}); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("missing Session credentials fell back", err)
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
