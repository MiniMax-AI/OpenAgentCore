package modelconfigurationpg_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
)

// The Store seals the bundle to its Harness as Core always has, so a bundle
// sealed before the Store owned the key still opens. A missing key is
// credentialcrypto.ErrUnavailable, and a wrong binding is a decryption
// failure, never a missing key or default.
func TestBundlesKeepTheirSealedFormat(t *testing.T) {
	f := newFixture(t)
	keyless := modelconfigurationpg.New(pgunit.NewPool(f.pool), nil)
	configuration := v1.ModelConfigurationInput{ModelProvider: fixtureProvider, Model: "fixture"}
	if _, err := keyless.LoadBundle(t.Context(), "codex"); !errors.Is(err, modelconfiguration.ErrNotFound) {
		t.Fatal("a missing default was not reported before the key", err)
	}
	record := modelconfiguration.Record{Harness: "codex", Provider: *fixtureProvider.SafeView(), Model: "fixture", HarnessConfig: json.RawMessage(`{}`), Configuration: configuration}
	if _, err := keyless.Replace(admin(t), record); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("a keyless Store sealed a bundle", err)
	}
	if listed := f.list(t); len(listed) != 0 {
		t.Fatal("a keyless replacement was stored", listed)
	}
	if _, err := f.adapter.Replace(admin(t), record); err != nil {
		t.Fatal(err)
	}
	loaded, err := f.adapter.LoadBundle(t.Context(), "codex")
	if err != nil || !reflect.DeepEqual(loaded.Configuration, configuration) {
		t.Fatal("the bundle did not round-trip", err)
	}
	if _, err := keyless.LoadBundle(t.Context(), "codex"); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("a keyless Store opened a bundle", err)
	}
	// write stores a bundle sealed the way Core sealed it before this Store
	// owned the key.
	write := func(harness string, bundle v1.ModelConfigurationInput) {
		t.Helper()
		raw, err := json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := f.cipher.SealDeploymentModelProvider(raw, harness)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), "UPDATE deployment_model_providers SET encrypted_config=$1 WHERE harness='codex'", sealed); err != nil {
			t.Fatal(err)
		}
	}
	old := configuration
	old.ModelProvider.APIKey = "sealed-before-the-move"
	write("codex", old)
	if loaded, err := f.adapter.LoadBundle(t.Context(), "codex"); err != nil || !reflect.DeepEqual(loaded.Configuration, old) {
		t.Fatal("a bundle sealed before the move did not open", err)
	}
	write("claude_code", old)
	if _, err := f.adapter.LoadBundle(t.Context(), "codex"); err == nil || err.Error() != "deployment model configuration decryption failed" {
		t.Fatal("a wrong binding was not a decryption failure", err)
	}
}
