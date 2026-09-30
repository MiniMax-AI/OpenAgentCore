package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestWebSetupCreatesManagerWithoutLocalProvider(t *testing.T) {
	t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", "")
	t.Setenv("OAC_INSTALLATION_ID", uuid.NewString())
	digest := sha256.Sum256([]byte("synthetic-admin"))
	path := filepath.Join(t.TempDir(), "core-key-digests.json")
	if err := os.WriteFile(path, []byte(`["`+hex.EncodeToString(digest[:])+`"]`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_CORE_KEY_DIGESTS_FILE", path)
	if _, err := configureManagedNodes(nil, "", nil); err == nil || !strings.Contains(err.Error(), "OAC_PUBLIC_URL") {
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
	t.Setenv("OAC_CORE_KEY_DIGESTS_FILE", "")
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
	s.publish(cached)
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
	t.Setenv("OAC_E2B_PROVIDER_BIN", filepath.Join(t.TempDir(), "missing-helper"))
	t.Setenv("OAC_E2B_STATE_DIR", t.TempDir())
	s := &managedSetup{installationID: id, store: &setupStore{value: store.SandboxSetup{
		InstallationID: id, Provider: "e2b", Mode: "direct", Generation: 1,
		Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString()},
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
	s.publish(previous)
	candidate, err := s.prepare(t.Context(), store.SandboxSetup{InstallationID: id, Provider: "microsandbox", Mode: "nodes", IdleSeconds: 300, RetentionSeconds: 86400})
	if err != nil {
		t.Fatal(err)
	}
	if s.selected.Load().Config != previous || candidate.Config.ProviderKind != "microsandbox" || candidate.Config.Suspension == nil || candidate.Config.CoreURL != "https://core.example/api/v1" {
		t.Fatal("preparation published or lost candidate configuration")
	}
	committed := *candidate.Config
	committed.Generation, committed.AdmissionPaused = 2, true
	candidate.Publish(&committed)
	if got := s.selected.Load(); got.Generation != 2 || got.Config.ProviderKind != "microsandbox" || !got.Config.AdmissionPaused {
		t.Fatal("commit did not publish the validated selection")
	}
}

func TestManagedSetupRejectedCandidateRetainsSelection(t *testing.T) {
	id := uuid.NewString()
	t.Setenv("OAC_E2B_PROVIDER_BIN", filepath.Join(t.TempDir(), "missing-helper"))
	t.Setenv("OAC_E2B_STATE_DIR", t.TempDir())
	s := &managedSetup{installationID: id}
	previous := &execution.RuntimeProvider{InstallationID: id, Generation: 1, ProviderKind: "docker"}
	s.publish(previous)
	_, err := s.prepare(t.Context(), store.SandboxSetup{InstallationID: id, Provider: "e2b", Mode: "direct",
		Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString()}})
	if !errors.Is(err, execution.ErrExecutionUnavailable) || s.selected.Load().Config != previous {
		t.Fatal("rejected candidate lost the previous selection", err)
	}
}

func TestE2BRequiresAPublicURLOutsideTheHost(t *testing.T) {
	id := uuid.NewString()
	s := &managedSetup{installationID: id, publicURL: "http://127.0.0.1:8091"}
	_, err := s.prepare(t.Context(), store.SandboxSetup{InstallationID: id, Provider: "e2b", Mode: "direct",
		Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString()}})
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

type delayedSetupStore struct {
	setupStore
	entered, release chan struct{}
}

func (s *delayedSetupStore) GetSandboxSetup(ctx context.Context) (store.SandboxSetup, error) {
	value := s.value
	close(s.entered)
	select {
	case <-s.release:
		return value, nil
	case <-ctx.Done():
		return store.SandboxSetup{}, ctx.Err()
	}
}
func TestManagedSetupResetTombstoneRejectsDelayedProviderLoad(t *testing.T) {
	id := uuid.NewString()
	db := &delayedSetupStore{setupStore: setupStore{value: store.SandboxSetup{InstallationID: id, Provider: "docker", Generation: 1}}, entered: make(chan struct{}), release: make(chan struct{})}
	s := &managedSetup{installationID: id, store: db}
	done := make(chan error, 1)
	go func() {
		provider, err := s.load(t.Context())
		if err == nil && provider != nil {
			err = errors.New("old provider survived reset")
		}
		done <- err
	}()
	<-db.entered
	s.publishUnconfigured(2)
	close(db.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.selected.Load().Generation != 2 || s.selected.Load().Config != nil || s.ObservationProviderType() != "" {
		t.Fatal("empty publication lost its generation")
	}
	s.publish(&execution.RuntimeProvider{Generation: 1, ProviderKind: "docker"})
	if s.selected.Load().Config != nil {
		t.Fatal("late old publication resurrected provider")
	}
	next := &execution.RuntimeProvider{Generation: 3, ProviderKind: "microsandbox"}
	s.publish(next)
	if s.selected.Load().Config != next || s.ObservationProviderType() != "microsandbox" {
		t.Fatal("reset blocked subsequent configuration")
	}
}

func (s *setupStore) GetSandboxAllocationSetup(_ context.Context, _ sandbox.Reference) (store.SandboxSetup, error) {
	return s.value, nil
}
func (s *setupStore) SandboxGenerationPage(context.Context, int64) ([]store.SandboxSetup, error) {
	return nil, nil
}
func (s *setupStore) SandboxCredentialAllocationPage(context.Context, string) ([]store.RuntimeAllocation, error) {
	return nil, nil
}

func (s *setupStore) ResolveRuntimeGeneration(context.Context, sandbox.Reference) (string, uint64, error) {
	return "", 0, errors.New("unexpected node generation lookup")
}
