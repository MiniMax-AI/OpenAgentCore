package store

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/google/uuid"
)

func TestTemplateEmptyUpdateTouchesTimeWithoutDecryptingOrChangingContents(t *testing.T) {
	keyless, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{37}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	tenant, name := uuid.NewString(), "Retained template"
	original, err := s.CreateEnvironmentTemplate(t.Context(), tenant, EnvironmentTemplateInput{
		Name:  &name,
		Files: []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/input.txt", Data: []byte("file-canary")}},
		Initialization: environmentconfig.Setup{
			Env:      map[string]string{"PRIVATE_SETUP": "env-canary"},
			Commands: []environmentconfig.SetupCommand{{Command: "echo setup-canary"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	readContents := func() []byte {
		t.Helper()
		var contents []byte
		if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(t) - 'updated_at' FROM environment_templates t WHERE id=$1", original.ID).Scan(&contents); err != nil {
			t.Fatal(err)
		}
		return contents
	}
	before := readContents()
	if _, err := keyless.UpdateEnvironmentTemplate(t.Context(), uuid.NewString(), original.ID, EnvironmentTemplateInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign empty update was admitted", err)
	}
	updated, err := keyless.UpdateEnvironmentTemplate(t.Context(), tenant, original.ID, EnvironmentTemplateInput{})
	if err != nil || !updated.UpdatedAt.After(original.UpdatedAt) {
		t.Fatal("empty update did not advance timestamp without a key", err)
	}
	original.UpdatedAt = updated.UpdatedAt
	if !reflect.DeepEqual(updated, original) || !bytes.Equal(before, readContents()) {
		t.Fatal("empty update changed template metadata, ciphertext or ownership")
	}
	retained, _, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, original.ID)
	if err != nil || retained.Initialization.Env["PRIVATE_SETUP"] != "env-canary" || len(retained.Initialization.Commands) != 1 {
		t.Fatal("empty update invalidated confidential setup", err)
	}
}
