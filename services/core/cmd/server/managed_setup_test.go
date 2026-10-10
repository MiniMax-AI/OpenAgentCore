package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/processconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/google/uuid"
)

func TestWebSetupCreatesManagerWithoutLocalProvider(t *testing.T) {
	origin, err := deployment.NewPublicOrigin("https://core.example")
	if err != nil {
		t.Fatal(err)
	}
	m := configureManagedNodes(nil, nil, providers.Builtin(), processconfig.Config{InstallationID: uuid.NewString(), PublicOrigin: origin}, func(context.Context) error { return nil })
	defer m.hub.Close()
	if m.setup == nil || m.hub == nil || m.runtime == nil || m.runtime.Provider != nil || m.setup.runtimeAPI != "https://core.example/api/v1" {
		t.Fatal("zero-node setup unexpectedly instantiated local compute or omitted management")
	}
}

// fakeDeploymentSetups is a strict deploymentSetups: a call without a set
// function fails the test.
type fakeDeploymentSetups struct {
	t                    testing.TB
	setup                func(context.Context) (deployment.Setup, error)
	allocationSetup      func(context.Context, sandbox.Reference) (deployment.Setup, error)
	generationPage       func(context.Context, int64) ([]deployment.Setup, error)
	withCredential       func(owner, candidate deployment.Setup) (deployment.Setup, error)
	allocationGeneration func(context.Context, sandbox.Reference) (string, uint64, error)
}

func (f *fakeDeploymentSetups) Setup(ctx context.Context) (deployment.Setup, error) {
	if f.setup == nil {
		return deployment.Setup{}, unexpectedCall(f.t, "Setup")
	}
	return f.setup(ctx)
}
func (f *fakeDeploymentSetups) AllocationSetup(ctx context.Context, ref sandbox.Reference) (deployment.Setup, error) {
	if f.allocationSetup == nil {
		return deployment.Setup{}, unexpectedCall(f.t, "AllocationSetup")
	}
	return f.allocationSetup(ctx, ref)
}
func (f *fakeDeploymentSetups) GenerationPage(ctx context.Context, after int64) ([]deployment.Setup, error) {
	if f.generationPage == nil {
		return nil, unexpectedCall(f.t, "GenerationPage")
	}
	return f.generationPage(ctx, after)
}
func (f *fakeDeploymentSetups) WithCredential(owner, candidate deployment.Setup) (deployment.Setup, error) {
	if f.withCredential == nil {
		return deployment.Setup{}, unexpectedCall(f.t, "WithCredential")
	}
	return f.withCredential(owner, candidate)
}
func (f *fakeDeploymentSetups) AllocationGeneration(ctx context.Context, ref sandbox.Reference) (string, uint64, error) {
	if f.allocationGeneration == nil {
		return "", 0, unexpectedCall(f.t, "AllocationGeneration")
	}
	return f.allocationGeneration(ctx, ref)
}

// fakeGenerationAllocations is a strict generationAllocations.
type fakeGenerationAllocations struct {
	t                     testing.TB
	credentialAllocations func(context.Context, string) ([]deployment.Allocation, error)
}

func (f *fakeGenerationAllocations) CredentialAllocations(ctx context.Context, after string) ([]deployment.Allocation, error) {
	if f.credentialAllocations == nil {
		return nil, unexpectedCall(f.t, "CredentialAllocations")
	}
	return f.credentialAllocations(ctx, after)
}

// unexpectedCall fails the test from any goroutine and returns the error the
// caller propagates.
func unexpectedCall(t testing.TB, method string) error {
	t.Errorf("unexpected call to %s", method)
	return errors.New("unexpected call to " + method)
}

// committedSetup reads *value as the deployment's committed setup.
func committedSetup(value *deployment.Setup) func(context.Context) (deployment.Setup, error) {
	return func(context.Context) (deployment.Setup, error) { return *value, nil }
}

// credentialService gives an owner setup a candidate's credential as the
// deployment service does. That needs no storage.
func credentialService(t *testing.T) func(owner, candidate deployment.Setup) (deployment.Setup, error) {
	t.Helper()
	rules, err := placement.NewRules(providers.Builtin(), "https://core.example")
	if err != nil {
		t.Fatal(err)
	}
	service, err := deployment.NewService(deploymentpg.New(nil, pgtest.CredentialKey(t)), deploymentpg.New(nil, pgtest.CredentialKey(t)), providers.Builtin(), rules)
	if err != nil {
		t.Fatal(err)
	}
	return service.WithCredential
}

func TestManagedSetupNeverReusesAnotherGenerationOrUnverifiedState(t *testing.T) {
	value := deployment.Setup{InstallationID: "installation", Provider: "docker", Mode: "nodes", Generation: 1}
	var loadErr error
	setups := &fakeDeploymentSetups{t: t, setup: func(context.Context) (deployment.Setup, error) { return value, loadErr }}
	s := &managedSetup{capacity: testSandboxCapacity(t), registry: providers.Builtin(), deployment: setups, allocations: &fakeGenerationAllocations{t: t}, installationID: "installation"}
	cached := &execution.RuntimeProvider{InstallationID: "installation", ProviderKind: "docker", Generation: 1}
	s.publish(cached)
	if got, err := s.load(t.Context()); err != nil || got != cached {
		t.Fatal("matching immutable selection was not reused")
	}
	loadErr = errors.New("database unavailable")
	if _, err := s.load(t.Context()); err == nil || errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("stale cached selection hid storage failure", err)
	}
	loadErr = deployment.ErrCredentialUnreadable
	if _, err := s.load(t.Context()); !errors.Is(err, execution.ErrExecutionUnavailable) || !errors.Is(err, deployment.ErrCredentialUnreadable) {
		t.Fatal("an unreadable credential must block execution without stopping Core", err)
	}
	loadErr = nil
	value.Generation = 2
	// No Hub is installed; a changed generation must construct again and fail.
	if _, err := s.load(t.Context()); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("changed provider availability must block execution without losing recovery", err)
	}
	value.InstallationID = "other-installation"
	value.Generation = 1
	if _, err := s.load(t.Context()); err == nil {
		t.Fatal("cache ignored installation identity")
	}
}

func TestMissingE2BHelperReportsProviderUnavailable(t *testing.T) {
	id := uuid.NewString()
	committed := deployment.Setup{InstallationID: id, Provider: "e2b", Mode: "direct", Generation: 1, UsesCredential: true,
		Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString()}}
	s := &managedSetup{capacity: testSandboxCapacity(t), processPaths: sandbox.ProcessPaths{ArtifactRoot: t.TempDir(), StateRoot: t.TempDir()}, registry: providers.Builtin(), installationID: id,
		deployment: &fakeDeploymentSetups{t: t, setup: committedSetup(&committed)}}
	if _, err := s.load(t.Context()); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("missing local helper must leave administrative recovery available", err)
	}
	if s.selected.Load() != nil {
		t.Fatal("unavailable provider was published")
	}
}

func TestManagedSetupPreparesWithoutPublishing(t *testing.T) {
	t.Setenv("OAC_SANDBOX_MAX_ACTIVE", "7")
	t.Setenv("OAC_SANDBOX_MAX_RETAINED", "31")
	id := uuid.NewString()
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	s := &managedSetup{capacity: testSandboxCapacity(t), registry: providers.Builtin(), installationID: id, hub: hub, deployment: &fakeDeploymentSetups{t: t}, allocations: &fakeGenerationAllocations{t: t}, runtimeAPI: "https://core.example/api/v1"}
	previous := &execution.RuntimeProvider{InstallationID: id, Generation: 1, ProviderKind: "docker"}
	s.publish(previous)
	candidate, err := s.prepare(t.Context(), deployment.Setup{InstallationID: id, Provider: "microsandbox", Mode: "nodes", Operations: microsandbox.Operations(), Suspension: &deployment.Suspension{IdleSeconds: 300, RetentionSeconds: 86400}})
	if err != nil {
		t.Fatal(err)
	}
	if s.selected.Load().Config != previous || candidate.Config.ProviderKind != "microsandbox" || candidate.Config.Suspension == nil || candidate.Config.CoreURL != "https://core.example/api/v1" {
		t.Fatal("preparation published or lost candidate configuration")
	}
	if candidate.Config.Suspension.MaxActive != 7 || candidate.Config.Suspension.MaxRetained != 31 {
		t.Fatal("configured capacity was not propagated")
	}
	committed := *candidate.Config
	committed.Generation = 2
	candidate.Publish(&committed)
	if got := s.selected.Load(); got.Generation != 2 || got.Config.ProviderKind != "microsandbox" {
		t.Fatal("commit did not publish the validated selection")
	}
}

func TestManagedSetupRejectedCandidateRetainsSelection(t *testing.T) {
	id := uuid.NewString()
	s := &managedSetup{capacity: testSandboxCapacity(t), processPaths: sandbox.ProcessPaths{ArtifactRoot: t.TempDir(), StateRoot: t.TempDir()}, registry: providers.Builtin(), installationID: id}
	previous := &execution.RuntimeProvider{InstallationID: id, Generation: 1, ProviderKind: "docker"}
	s.publish(previous)
	_, err := s.prepare(t.Context(), deployment.Setup{InstallationID: id, Provider: "e2b", Mode: "direct", UsesCredential: true,
		Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString()}})
	if !errors.Is(err, execution.ErrExecutionUnavailable) || s.selected.Load().Config != previous {
		t.Fatal("rejected candidate lost the previous selection", err)
	}
}

func TestManagedSetupResetTombstoneRejectsDelayedProviderLoad(t *testing.T) {
	id := uuid.NewString()
	value := deployment.Setup{InstallationID: id, Provider: "docker", Mode: "nodes", Generation: 1}
	entered, release := make(chan struct{}), make(chan struct{})
	delayed := func(ctx context.Context) (deployment.Setup, error) {
		close(entered)
		select {
		case <-release:
			return value, nil
		case <-ctx.Done():
			return deployment.Setup{}, ctx.Err()
		}
	}
	s := &managedSetup{capacity: testSandboxCapacity(t), registry: providers.Builtin(), installationID: id, deployment: &fakeDeploymentSetups{t: t, setup: delayed}}
	done := make(chan error, 1)
	go func() {
		provider, err := s.load(t.Context())
		if err == nil && provider != nil {
			err = errors.New("old provider survived reset")
		}
		done <- err
	}()
	<-entered
	s.publishUnconfigured(2)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.selected.Load().Generation != 2 || s.selected.Load().Config != nil {
		t.Fatal("empty publication lost its generation")
	}
	s.publish(&execution.RuntimeProvider{Generation: 1, ProviderKind: "docker"})
	if s.selected.Load().Config != nil {
		t.Fatal("late old publication resurrected provider")
	}
	next := &execution.RuntimeProvider{Generation: 3, ProviderKind: "microsandbox"}
	s.publish(next)
	if s.selected.Load().Config != next {
		t.Fatal("reset blocked subsequent configuration")
	}
}

func testProviderPaths(t *testing.T, helper, state string) sandbox.ProcessPaths {
	t.Helper()
	paths := sandbox.ProcessPaths{ArtifactRoot: t.TempDir(), StateRoot: filepath.Dir(state)}
	binary, resolvedState, err := e2b.InstalledPaths(paths)
	if err != nil || resolvedState != state {
		t.Fatalf("provider layout: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, raw, 0700); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestManagedObservationSourceKeepsSelectionAcrossReconfiguration(t *testing.T) {
	value := deployment.Setup{InstallationID: "installation", Generation: 1}
	setup := &managedSetup{registry: providers.Builtin(), deployment: &fakeDeploymentSetups{t: t, setup: committedSetup(&value)}, installationID: "installation"}
	if source, kind, err := setup.observationSource(t.Context()); source != nil || kind != "" || !errors.Is(err, runtimeobs.ErrUnavailable) {
		t.Fatal("unconfigured setup did not return typed unavailability", source, err)
	}
	first := &docker.Provider{}
	value.Provider, value.Mode, value.Generation = "docker", "nodes", 2
	setup.publish(&execution.RuntimeProvider{Generation: 2, ProviderKind: "docker", Provider: first})
	source, kind, err := setup.observationSource(t.Context())
	if err != nil || source != first || kind != "docker" {
		t.Fatal(source, kind, err)
	}
	next := &microsandbox.Provider{}
	value.Provider, value.Mode, value.Generation = "microsandbox", "nodes", 3
	setup.publish(&execution.RuntimeProvider{Generation: 3, ProviderKind: "microsandbox", Provider: next})
	selected, kind, err := setup.observationSource(t.Context())
	if err != nil || selected != next || kind != "microsandbox" {
		t.Fatal(selected, kind, err)
	}
}
