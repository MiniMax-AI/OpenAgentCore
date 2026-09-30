package store

import (
	"bytes"
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestSessionModelExecutionStoresOnlyProviderBundle(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st := NewWithCredentialCipher(pool, cipher)
	ctx, tenant := t.Context(), uuid.NewString()
	provider := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.com/v1", APIKey: "provider-key-canary"}
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: []byte(`{"agent":{"model":"actual-model"},"environment":{"type":"openai_hosted"}}`), ModelProvider: provider}
	session, err := st.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	flat, err := st.SessionModelExecution(ctx, tenant, session.ID)
	if err != nil || flat == nil || *flat != *provider {
		t.Fatal("new Session did not store a flat provider bundle", err)
	}
	options := map[string]any{"codex_provider": map[string]any{
		"base_url": provider.BaseURL, "bearer_token": provider.APIKey, "wire_api": "responses",
		"http_headers": map[string]any{"x-deployment-secret": "header-secret-canary"},
	}, "mode": "trusted-deployment-mode"}
	historical, err := json.Marshal(struct {
		v1.ModelProviderInput
		NativeOptions map[string]any `json:"native_options"`
	}{*provider, options})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.SealModelExecution(historical, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE session_model_execution SET encrypted_config=$2 WHERE session_id=$1", session.ID, ciphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithCredentialCipher(pool, cipher).SessionModelExecution(ctx, tenant, session.ID); err == nil {
		t.Fatal("retired native options accepted in provider bundle")
	}
}
