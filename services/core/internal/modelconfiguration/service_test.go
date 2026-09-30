package modelconfiguration

import (
	"context"
	"errors"
	"reflect"
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
	loadBundle func(context.Context, string) (Bundle, error)
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

func (f *fakeStorage) LoadBundle(ctx context.Context, harness string) (Bundle, error) {
	if f.loadBundle == nil {
		f.t.Fatal("unexpected call to LoadBundle")
	}
	return f.loadBundle(ctx, harness)
}

func validConfiguration() v1.ModelConfigurationInput {
	return v1.ModelConfigurationInput{ModelProvider: v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.example/v1", APIKey: "secret-key"}, Model: "fixture-model"}
}

func newService(t *testing.T, storage Storage) *Service {
	t.Helper()
	service, err := NewService(storage)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestNewServiceRequiresStorage(t *testing.T) {
	if _, err := NewService(nil); err == nil {
		t.Fatal("nil storage accepted")
	}
}

func TestReplacePassesTheCompleteBundleWithItsSafeColumns(t *testing.T) {
	stored := Configuration{Harness: "codex", Model: "fixture-model"}
	var record Record
	storage := &fakeStorage{t: t, replace: func(_ context.Context, r Record) (Configuration, error) {
		record = r
		return stored, nil
	}}
	result, err := newService(t, storage).Replace(t.Context(), Replacement{Harness: "codex", Configuration: validConfiguration()})
	if err != nil || result.Harness != stored.Harness || result.Model != stored.Model {
		t.Fatal(result, err)
	}
	if record.Harness != "codex" || record.Model != "fixture-model" || string(record.HarnessConfig) != "{}" ||
		record.Provider != (v1.ModelProviderView{Protocol: "responses", BaseURL: "https://model.example/v1", APIKeyConfigured: true}) {
		t.Fatalf("safe columns: %+v", record)
	}
	if !reflect.DeepEqual(record.Configuration, validConfiguration()) {
		t.Fatalf("bundle: %+v", record.Configuration)
	}
	storage.replace = func(context.Context, Record) (Configuration, error) {
		return Configuration{}, credentialcrypto.ErrUnavailable
	}
	if _, err := newService(t, storage).Replace(t.Context(), Replacement{Harness: "codex", Configuration: validConfiguration()}); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("missing credential key", err)
	}
}

func TestReplaceRejectsBeforeStorage(t *testing.T) {
	invalid := validConfiguration()
	invalid.ModelProvider.Protocol = "anthropic-unknown"
	var field *v1.ModelProviderError
	if _, err := newService(t, &fakeStorage{t: t}).Replace(t.Context(), Replacement{Harness: "codex", Configuration: invalid}); !errors.As(err, &field) || field.Param != "protocol" {
		t.Fatal("invalid configuration", err)
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
	if err := newService(t, storage).Delete(t.Context(), "codex"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestResolveValidatesTheOpenedBundle(t *testing.T) {
	revision := uuid.New()
	loaded := func(configuration v1.ModelConfigurationInput, err error) *fakeStorage {
		return &fakeStorage{t: t, loadBundle: func(_ context.Context, harness string) (Bundle, error) {
			if harness != "codex" {
				t.Fatal(harness)
			}
			return Bundle{Configuration: configuration, Revision: revision}, err
		}}
	}
	snapshot, err := newService(t, loaded(validConfiguration(), nil)).Resolve(t.Context(), "codex")
	if err != nil || snapshot == nil || snapshot.Revision != revision || snapshot.Provider.APIKey != "secret-key" || snapshot.Model != "fixture-model" || string(snapshot.HarnessConfig) != "{}" {
		t.Fatal("snapshot", snapshot, err)
	}
	if snapshot, err := newService(t, loaded(v1.ModelConfigurationInput{}, ErrNotFound)).Resolve(t.Context(), "codex"); snapshot != nil || err != nil {
		t.Fatal("missing default", snapshot, err)
	}
	for _, failure := range []error{errors.New("storage failed"), credentialcrypto.ErrUnavailable} {
		if _, err := newService(t, loaded(v1.ModelConfigurationInput{}, failure)).Resolve(t.Context(), "codex"); !errors.Is(err, failure) {
			t.Fatal("storage failure", err)
		}
	}
	invalid := validConfiguration()
	invalid.Model = ""
	var field *v1.ModelProviderError
	if _, err := newService(t, loaded(invalid, nil)).Resolve(t.Context(), "codex"); !errors.As(err, &field) {
		t.Fatal("unsupported stored configuration", err)
	}
}
