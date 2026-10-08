package integration

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func localEnvironment(t *testing.T, s *Store, tenant string) (sessions.Session, sessions.Environment) {
	t.Helper()
	session, err := s.CreateSession(t.Context(), tenant, sessions.CreateSession{
		Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := sessionAdapter(s).GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return session, environment
}
