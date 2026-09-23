package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestSessionDeploymentModelOptionsEncryptedAndFrozen(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st := NewWithCredentialCipher(pool, cipher)
	ctx, tenant := t.Context(), uuid.NewString()
	options := map[string]any{"codex_provider": map[string]any{
		"base_url": "https://example.com/v1", "bearer_token": "provider-key-canary", "wire_api": "responses",
		"http_headers": map[string]any{"x-deployment-secret": "header-secret-canary"},
		"query_params": map[string]any{"api-version": "2026-01-01"},
	}, "mode": "trusted-deployment-mode"}
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: []byte(`{"agent":{"model":"actual-model"},"environment":{"type":"openai_hosted"}}`), ModelProvider: &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.com/v1", APIKey: "provider-key-canary"}, ModelOptions: options}
	session, err := st.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err := pool.QueryRow(ctx, "SELECT encrypted_config FROM session_model_execution WHERE session_id=$1", session.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"provider-key-canary", "header-secret-canary", "native_options", "http_headers", "query_params", "trusted-deployment-mode"} {
		if bytes.Contains(ciphertext, []byte(private)) || bytes.Contains(session.Configuration, []byte(private)) {
			t.Fatal("deployment options exposed in plaintext")
		}
	}
	restarted := NewWithCredentialCipher(pool, cipher)
	provider, frozen, err := restarted.SessionModelExecutionWithOptions(ctx, tenant, session.ID)
	if err != nil || provider == nil || *provider != *input.ModelProvider || !reflect.DeepEqual(frozen, options) {
		t.Fatal("restart changed encrypted deployment options", err)
	}
	// Updating an operator's in-memory options cannot affect the saved Session.
	options["codex_provider"].(map[string]any)["http_headers"].(map[string]any)["x-deployment-secret"] = "rotated-header-canary"
	options["mode"] = "new-mode"
	_, again, err := restarted.SessionModelExecutionWithOptions(ctx, tenant, session.ID)
	if err != nil || !reflect.DeepEqual(again, frozen) {
		t.Fatal("operator edit changed frozen Session options", err)
	}
	if _, err := st.CreateSession(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed deployment options accepted as identical retry", err)
	}
	input.ModelOptions = frozen
	if replay, err := st.CreateSession(ctx, tenant, input); err != nil || replay.ID != session.ID {
		t.Fatal("identical options changed retry identity", err)
	}
	input.IdempotencyKey = uuid.NewString()
	input.ModelProvider = nil
	if _, err := st.CreateSession(ctx, tenant, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("provider-free native options accepted", err)
	}
	// Existing flat provider ciphertext remains readable without native options.
	flat, err := json.Marshal(provider)
	if err != nil {
		t.Fatal(err)
	}
	flatCiphertext, err := cipher.SealModelExecution(flat, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE session_model_execution SET encrypted_config=$2 WHERE session_id=$1", session.ID, flatCiphertext); err != nil {
		t.Fatal(err)
	}
	legacy, legacyOptions, err := restarted.SessionModelExecutionWithOptions(ctx, tenant, session.ID)
	if err != nil || legacy == nil || *legacy != *provider || legacyOptions != nil {
		t.Fatal("legacy flat provider snapshot could not be read", err)
	}
}
