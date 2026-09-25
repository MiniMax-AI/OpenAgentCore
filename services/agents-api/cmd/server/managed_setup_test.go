package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"errors"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestWebSetupCreatesManagerWithoutLocalProvider(t *testing.T) {
	t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", "")
	t.Setenv("AGENTS_API_SANDBOX_INSTALLATION_ID", uuid.NewString())
	digest := sha256.Sum256([]byte("synthetic-admin"))
	path := filepath.Join(t.TempDir(), "core-key-digests.json")
	if err := os.WriteFile(path, []byte(`["`+hex.EncodeToString(digest[:])+`"]`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTS_API_CORE_KEY_DIGESTS_FILE", path)
	if _, err := configureManagedNodes(nil, "", nil); err == nil || !strings.Contains(err.Error(), "AGENTS_API_PUBLIC_URL") {
		t.Fatal("sandbox manager started without a public URL", err)
	}
	m, err := configureManagedNodes(nil, "https://core.example", func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	if m.setup == nil || m.admin == nil || m.hub == nil || m.runtime == nil || m.runtime.Provider != nil {
		t.Fatal("zero-node setup unexpectedly instantiated local compute or omitted management")
	}
	t.Setenv("AGENTS_API_CORE_KEY_DIGESTS_FILE", "")
	if _, err := configureManagedNodes(nil, "https://core.example", nil); err == nil {
		t.Fatal("setup accepted without admin authentication")
	}
}

type setupStore struct {
	value store.SandboxSetup
	err   error
}

func (s *setupStore) GetSandboxSetup(context.Context) (store.SandboxSetup, error) {
	return s.value, s.err
}
func (*setupStore) ResolveRuntimeNode(context.Context, string, string) (string, error) {
	return "", errors.New("unexpected node lookup")
}

func TestManagedSetupNeverReusesAnotherGenerationOrUnverifiedState(t *testing.T) {
	db := &setupStore{value: store.SandboxSetup{InstallationID: "installation", Provider: "docker", Generation: 1}}
	s := &managedSetup{store: db, installationID: "installation"}
	cached := &execution.RuntimeProvider{InstallationID: "installation", ProviderKind: "docker", Generation: 1}
	s.selected.Store(cached)
	if got, err := s.load(t.Context()); err != nil || got != cached {
		t.Fatal("matching immutable selection was not reused")
	}
	db.err = errors.New("database unavailable")
	if _, err := s.load(t.Context()); err == nil {
		t.Fatal("stale cached selection hid storage failure")
	}
	db.err = nil
	db.value.Generation = 2
	// No Hub is installed; a changed generation must construct again and fail.
	if _, err := s.load(t.Context()); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("changed provider availability must block execution without losing recovery", err)
	}
	db.value.InstallationID = "other-installation"
	db.value.Generation = 1
	if _, err := s.load(t.Context()); err == nil {
		t.Fatal("cache ignored installation identity")
	}
}

func TestMissingE2BHelperReportsProviderUnavailable(t *testing.T) {
	id := uuid.NewString()
	t.Setenv("AGENTS_API_E2B_PROVIDER_BIN", filepath.Join(t.TempDir(), "missing-helper"))
	t.Setenv("AGENTS_API_E2B_STATE_DIR", t.TempDir())
	s := &managedSetup{installationID: id, store: &setupStore{value: store.SandboxSetup{
		InstallationID: id, Provider: "e2b", Mode: "direct", Generation: 1,
		E2B: &store.SandboxE2BConfiguration{APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString()},
	}}}
	if _, err := s.load(t.Context()); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("missing local helper must leave administrative recovery available", err)
	}
	if s.selected.Load() != nil {
		t.Fatal("unavailable provider was published")
	}
}

func TestManagedSetupPreparesWithoutPublishing(t *testing.T) {
	id := uuid.NewString()
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	s := &managedSetup{installationID: id, hub: hub, store: &setupStore{}, publicURL: "https://core.example"}
	previous := &execution.RuntimeProvider{InstallationID: id, Generation: 1, ProviderKind: "docker"}
	s.selected.Store(previous)
	candidate, err := s.prepare(t.Context(), store.SandboxSetup{InstallationID: id, Provider: "microsandbox", Mode: "nodes", IdleSeconds: 300, RetentionSeconds: 86400})
	if err != nil {
		t.Fatal(err)
	}
	if s.selected.Load() != previous || candidate.Config.ProviderKind != "microsandbox" || candidate.Config.Suspension == nil || candidate.Config.CoreURL != "https://core.example/api/v1" {
		t.Fatal("preparation published or lost candidate configuration")
	}
	committed := *candidate.Config
	committed.Generation, committed.Maintenance = 2, true
	candidate.Publish(&committed)
	if got := s.selected.Load(); got.Generation != 2 || got.ProviderKind != "microsandbox" || !got.Maintenance {
		t.Fatal("commit did not publish the validated selection")
	}
}

func TestManagedSetupRejectedCandidateRetainsSelection(t *testing.T) {
	id := uuid.NewString()
	t.Setenv("AGENTS_API_E2B_PROVIDER_BIN", filepath.Join(t.TempDir(), "missing-helper"))
	t.Setenv("AGENTS_API_E2B_STATE_DIR", t.TempDir())
	s := &managedSetup{installationID: id}
	previous := &execution.RuntimeProvider{InstallationID: id, Generation: 1, ProviderKind: "docker"}
	s.selected.Store(previous)
	_, err := s.prepare(t.Context(), store.SandboxSetup{InstallationID: id, Provider: "e2b", Mode: "direct",
		E2B: &store.SandboxE2BConfiguration{APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString()}})
	if !errors.Is(err, execution.ErrExecutionUnavailable) || s.selected.Load() != previous {
		t.Fatal("rejected candidate lost the previous selection", err)
	}
}

func TestE2BRequiresAPublicURLOutsideTheHost(t *testing.T) {
	id := uuid.NewString()
	s := &managedSetup{installationID: id, publicURL: "http://127.0.0.1:8091"}
	_, err := s.prepare(t.Context(), store.SandboxSetup{InstallationID: id, Provider: "e2b", Mode: "direct",
		E2B: &store.SandboxE2BConfiguration{APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString()}})
	if !errors.Is(err, store.ErrSandboxPublicURLUnreachable) {
		t.Fatal("E2B accepted a loopback public URL", err)
	}
}

func TestCoreRejectsFileManagedSandboxConfiguration(t *testing.T) {
	t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", "/retained/config.json")
	if _, err := configureManagedNodes(nil, "https://core.example", nil); err == nil {
		t.Fatal("accepted a second configuration source")
	}
}
