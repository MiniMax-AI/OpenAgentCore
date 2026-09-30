package modelconfiguration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/google/uuid"
)

// fakeStorage fails the test on any call whose func is not set.
type fakeStorage struct {
	t          *testing.T
	replace    func(context.Context, Record) (Configuration, error)
	delete     func(context.Context, string) error
	loadSealed func(context.Context, string) (Sealed, error)
}

func (f *fakeStorage) Replace(ctx context.Context, record Record) (Configuration, error) {
	if f.replace == nil {
		f.t.Fatal("unexpected call to Replace")
	}
	return f.replace(ctx, record)
}

func (f *fakeStorage) Delete(ctx context.Context, harness string) error {
	if f.delete == nil {
		f.t.Fatal("unexpected call to Delete")
	}
	return f.delete(ctx, harness)
}

func (f *fakeStorage) LoadSealed(ctx context.Context, harness string) (Sealed, error) {
	if f.loadSealed == nil {
		f.t.Fatal("unexpected call to LoadSealed")
	}
	return f.loadSealed(ctx, harness)
}

func testCipher(t *testing.T, fill byte) *credentialcrypto.Cipher {
	t.Helper()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func validConfiguration() v1.ModelConfigurationInput {
	return v1.ModelConfigurationInput{ModelProvider: v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.example/v1", APIKey: "secret-key"}, Model: "fixture-model"}
}

func newService(t *testing.T, storage Storage, cipher *credentialcrypto.Cipher) *Service {
	t.Helper()
	service, err := NewService(storage, cipher)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestNewServiceRequiresStorage(t *testing.T) {
	if _, err := NewService(nil, testCipher(t, 1)); err == nil {
		t.Fatal("nil storage accepted")
	}
}

func TestReplaceSealsTheCompleteBundleForItsHarness(t *testing.T) {
	cipher := testCipher(t, 1)
	stored := Configuration{Harness: "codex", Model: "fixture-model"}
	var record Record
	storage := &fakeStorage{t: t, replace: func(_ context.Context, r Record) (Configuration, error) {
		record = r
		return stored, nil
	}}
	result, err := newService(t, storage, cipher).Replace(t.Context(), Replacement{Harness: "codex", Configuration: validConfiguration()})
	if err != nil || result.Harness != stored.Harness || result.Model != stored.Model {
		t.Fatal(result, err)
	}
	if record.Harness != "codex" || record.Model != "fixture-model" || string(record.HarnessConfig) != "{}" ||
		record.Provider != (v1.ModelProviderView{Protocol: "responses", BaseURL: "https://model.example/v1", APIKeyConfigured: true}) {
		t.Fatalf("safe columns: %+v", record)
	}
	if bytes.Contains(record.Sealed, []byte("secret-key")) {
		t.Fatal("key stored in plaintext")
	}
	if _, err := cipher.OpenDeploymentModelProvider(record.Sealed, "claude_code"); err == nil {
		t.Fatal("bundle opens for another harness")
	}
	raw, err := cipher.OpenDeploymentModelProvider(record.Sealed, "codex")
	var opened v1.ModelConfigurationInput
	if err != nil || json.Unmarshal(raw, &opened) != nil || opened.ModelProvider.APIKey != "secret-key" || opened.Model != "fixture-model" {
		t.Fatal("sealed bundle incomplete", err)
	}
}

func TestReplaceRejectsBeforeStorage(t *testing.T) {
	invalid := validConfiguration()
	invalid.ModelProvider.Protocol = "anthropic-unknown"
	var field *v1.ModelProviderError
	if _, err := newService(t, &fakeStorage{t: t}, testCipher(t, 1)).Replace(t.Context(), Replacement{Harness: "codex", Configuration: invalid}); !errors.As(err, &field) || field.Param != "protocol" {
		t.Fatal("invalid configuration", err)
	}
	if _, err := newService(t, &fakeStorage{t: t}, nil).Replace(t.Context(), Replacement{Harness: "codex", Configuration: validConfiguration()}); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("missing credential key", err)
	}
}

func TestDeletePassesTheHarness(t *testing.T) {
	failure := errors.New("storage failed")
	storage := &fakeStorage{t: t, delete: func(_ context.Context, harness string) error {
		if harness != "codex" {
			t.Fatal(harness)
		}
		return failure
	}}
	if err := newService(t, storage, nil).Delete(t.Context(), "codex"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestResolveOpensTheStoredBundle(t *testing.T) {
	cipher := testCipher(t, 1)
	seal := func(harness string, configuration any) []byte {
		raw, _ := json.Marshal(configuration)
		sealed, err := cipher.SealDeploymentModelProvider(raw, harness)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	revision := uuid.New()
	loaded := func(bundle []byte, err error) *fakeStorage {
		return &fakeStorage{t: t, loadSealed: func(_ context.Context, harness string) (Sealed, error) {
			if harness != "codex" {
				t.Fatal(harness)
			}
			return Sealed{Bundle: bundle, Revision: revision}, err
		}}
	}
	snapshot, err := newService(t, loaded(seal("codex", validConfiguration()), nil), cipher).Resolve(t.Context(), "codex")
	if err != nil || snapshot == nil || snapshot.Revision != revision || snapshot.Provider.APIKey != "secret-key" || snapshot.Model != "fixture-model" || string(snapshot.HarnessConfig) != "{}" {
		t.Fatal("snapshot", snapshot, err)
	}
	if snapshot, err := newService(t, loaded(nil, ErrNotFound), cipher).Resolve(t.Context(), "codex"); snapshot != nil || err != nil {
		t.Fatal("missing default", snapshot, err)
	}
	failure := errors.New("storage failed")
	if _, err := newService(t, loaded(nil, failure), cipher).Resolve(t.Context(), "codex"); !errors.Is(err, failure) {
		t.Fatal("storage failure", err)
	}
	invalid := validConfiguration()
	invalid.Model = ""
	var field *v1.ModelProviderError
	if _, err := newService(t, loaded(seal("codex", invalid), nil), cipher).Resolve(t.Context(), "codex"); !errors.As(err, &field) {
		t.Fatal("unsupported stored configuration", err)
	}
	for name, service := range map[string]*Service{
		"no key":        newService(t, loaded(seal("codex", validConfiguration()), nil), nil),
		"wrong key":     newService(t, loaded(seal("codex", validConfiguration()), nil), testCipher(t, 2)),
		"other harness": newService(t, loaded(seal("claude_code", validConfiguration()), nil), cipher),
		"not JSON":      newService(t, loaded(seal("codex", "not an object"), nil), cipher),
	} {
		if _, err := service.Resolve(t.Context(), "codex"); !errors.Is(err, credentialcrypto.ErrUnavailable) {
			t.Fatal(name, err)
		}
	}
}
