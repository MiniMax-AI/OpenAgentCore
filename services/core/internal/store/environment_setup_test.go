package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestEnvironmentSetupEncryptedSnapshotAndIsolation(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := withPlacement(t, NewWithCredentialCipher(pool, cipher))
	tenant, foreign := uuid.NewString(), uuid.NewString()
	setup := environmentconfig.Setup{Env: map[string]string{"SECRET": "session-env-canary"}, Commands: []environmentconfig.SetupCommand{{Command: "printf session-command-canary > result"}}, Packages: v1.EnvironmentPackages{NPM: []string{"is-number@7.0.0"}}}
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: setup}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(session.Configuration, []byte("canary")) {
		t.Fatal("plaintext Session metadata")
	}
	frozen, err := sessionAdapter(s).ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(frozen, setup) {
		t.Fatal("Session did not freeze setup", err)
	}
	if _, err := sessionAdapter(s).ReadEnvironmentSetup(t.Context(), foreign, session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign Session initialization", err)
	}
	retry, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || retry.ID != session.ID {
		t.Fatal("creation replay", err)
	}
	input.Initialization.Env = map[string]string{"SECRET": "changed"}
	if _, err := s.CreateSession(t.Context(), tenant, input); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("changed setup retried", err)
	}
	if _, err := s.GetSessionExecutionBinding(t.Context(), tenant, session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("uninitialized execution admitted", err)
	}
}
