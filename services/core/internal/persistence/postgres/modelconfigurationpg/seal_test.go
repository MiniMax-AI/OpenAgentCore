package modelconfigurationpg_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
)

// The Store seals only the provider key to its Harness. A key sealed under
// another key or binding is a decryption failure, never a missing default.
func TestOnlyTheKeyIsSealed(t *testing.T) {
	f := newFixture(t)
	replaced := modelconfigurationpg.New(pgunit.NewPool(f.pool), pgtest.CredentialKey(t))
	configuration := v1.ModelConfigurationInput{ModelProvider: fixtureProvider, Model: "fixture"}
	if _, err := replaced.LoadBundle(t.Context(), "codex"); !errors.Is(err, modelconfiguration.ErrNotFound) {
		t.Fatal("a missing default was not reported before decryption", err)
	}
	record := modelconfiguration.Record{Harness: "codex", Provider: *fixtureProvider.SafeView(), Model: "fixture", HarnessConfig: json.RawMessage(`{}`), APIKey: fixtureProvider.APIKey}
	if _, err := f.adapter.Replace(admin(t), record); err != nil {
		t.Fatal(err)
	}
	loaded, err := f.adapter.LoadBundle(t.Context(), "codex")
	configuration.HarnessConfig = json.RawMessage(`{}`)
	if err != nil || !reflect.DeepEqual(loaded.Configuration, configuration) {
		t.Fatal("the default did not round-trip", loaded.Configuration, err)
	}
	var sealed []byte
	if err := f.pool.QueryRow(t.Context(), "SELECT encrypted_api_key FROM deployment_model_providers WHERE harness='codex'").Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(fixtureProvider.BaseURL)) {
		t.Fatal("the sealed value holds more than the key", err)
	}
	if _, err := replaced.LoadBundle(t.Context(), "codex"); err == nil || err.Error() != "deployment model configuration decryption failed" {
		t.Fatal("a replaced key opened a bundle", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE deployment_model_providers SET harness='claude_code' WHERE harness='codex'"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.adapter.LoadBundle(t.Context(), "claude_code"); err == nil || err.Error() != "deployment model configuration decryption failed" {
		t.Fatal("a wrong binding was not a decryption failure", err)
	}
}
