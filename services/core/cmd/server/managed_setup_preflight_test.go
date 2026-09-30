package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestE2BRejectedSpecificationHasSafeActionableDiagnostic(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "provider")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"Version\":1,\"ErrorCode\":\"invalid\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_E2B_PROVIDER_BIN", helper)
	t.Setenv("OAC_E2B_STATE_DIR", state)
	id := uuid.NewString()
	s := &managedSetup{installationID: id}
	selection := store.SandboxSetup{InstallationID: id, Provider: "e2b", Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 3, MemoryMiB: 3072}}, E2B: &sandbox.E2BConfiguration{APIKey: "synthetic-private-key", Template: "runtime:" + uuid.NewString()}}
	_, err := s.prepare(t.Context(), selection)
	if !errors.Is(err, e2b.ErrTemplateInvalid) || strings.Contains(err.Error(), "synthetic-private-key") || s.selected.Load() != nil {
		t.Fatal("rejected candidate lost its safe diagnostic or was published", err)
	}
	s.store = &setupStore{value: selection}
	if restored, err := s.load(t.Context()); err != nil || restored == nil || restored.Provider == nil {
		t.Fatal("template rejection prevented loading committed resource ownership", err)
	}
}

func TestCommittedE2BResourceAccessDoesNotRequireTemplateLookup(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "provider")
	const script = `#!/usr/bin/env python3
import json, pathlib, sys
q = json.load(sys.stdin)
op = q['Operation']
with (pathlib.Path(q['Config']['StateDir']) / 'operations').open('a') as log:
    log.write(op + '\n')
if op == 'validate_deployment':
    print(json.dumps({'Version': 1, 'ErrorCode': 'invalid'}))
else:
    info = dict(q['Reference'], ProviderID='owned-compute', State='stopped', CreateSettled=True)
    if op == 'kill':
        info.update(ProviderID='', State='absent')
    print(json.dumps({'Version': 1, 'Info': info}))
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_E2B_PROVIDER_BIN", helper)
	t.Setenv("OAC_E2B_STATE_DIR", state)
	id := uuid.NewString()
	selection := store.SandboxSetup{InstallationID: id, Provider: "e2b", Generation: 1,
		Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}},
		E2B:           &sandbox.E2BConfiguration{APIKey: "synthetic-private-key", Template: "runtime:" + uuid.NewString()}}
	s := &managedSetup{installationID: id, store: &setupStore{value: selection}}
	if _, err := s.prepare(t.Context(), selection); err == nil || s.selected.Load() != nil {
		t.Fatal("invalid new template selection was published", err)
	}
	loaded, err := s.load(t.Context())
	if err != nil || loaded == nil {
		t.Fatal("committed provider became inaccessible", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ref := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	info, err := loaded.Provider.GetInfo(ctx, ref)
	if err != nil || info.Reference != ref || info.ProviderID != "owned-compute" {
		t.Fatal("could not observe retained resource", info, err)
	}
	if err := loaded.Provider.Kill(ctx, ref); err != nil {
		t.Fatal("could not clean retained resource", err)
	}
	operations, err := os.ReadFile(filepath.Join(state, "operations"))
	if err != nil || string(operations) != "validate_deployment\ninspect\nkill\n" {
		t.Fatal("loading or cleanup repeated candidate-template validation", string(operations), err)
	}
}

func TestE2BCandidateAdoptsTemplateBuildForOmittedResources(t *testing.T) {
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "provider")
	requests := filepath.Join(state, "requests")
	script := "#!/bin/sh\ncat >>" + requests + "\necho >>" + requests + "\nprintf '%s' '{\"Version\":1,\"ErrorCode\":\"\",\"DeploymentValid\":true,\"TemplateBuild\":{\"Status\":\"ready\",\"CPUs\":4,\"MemoryMiB\":4096,\"RootDiskMiB\":24063}}'\n"
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_E2B_PROVIDER_BIN", helper)
	t.Setenv("OAC_E2B_STATE_DIR", state)
	id := uuid.NewString()
	s := &managedSetup{installationID: id, store: &setupStore{}}
	selection := store.SandboxSetup{InstallationID: id, Provider: "e2b", E2B: &sandbox.E2BConfiguration{APIKey: "synthetic-private-key", Template: "runtime:" + uuid.NewString()}}
	candidate, err := s.prepare(t.Context(), selection)
	disk := int32(24063)
	if err != nil || candidate.Selection.E2B.TemplateBuild == nil || candidate.Selection.E2B.TemplateBuild.CPUs != 4 || candidate.Selection.E2B.TemplateBuild.MemoryMiB != 4096 ||
		*candidate.Selection.E2B.TemplateBuild.RootDiskMiB != disk || candidate.Selection.E2B.TemplateBuild.Status != "ready" {
		t.Fatalf("validated build was not recorded: %+v %v", candidate.Selection.E2B.TemplateBuild, err)
	}
	// The published candidate enforces the adopted resources.
	selection.Specification.Resources = sandbox.Resources{CPUs: 4, MemoryMiB: 4096}
	provider, err := s.provider(selection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.(*e2b.Provider).ValidateDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	logged, err := os.ReadFile(requests)
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if err != nil || len(lines) != 2 || !strings.Contains(lines[0], `"Resources":null`) || !strings.Contains(lines[1], `"Resources":{"cpus":4,"memory_mib":4096}`) {
		t.Fatalf("omitted resources were not adopted from the build: %s %v", logged, err)
	}
}

func TestInitialE2BPublicTemplateOutsideTeamIsRejected(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "provider")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"Version\":1,\"ErrorCode\":\"team_mismatch\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_E2B_PROVIDER_BIN", helper)
	t.Setenv("OAC_E2B_STATE_DIR", state)
	id := uuid.NewString()
	s := &managedSetup{installationID: id, store: &setupStore{}}
	selection := store.SandboxSetup{InstallationID: id, Provider: "e2b", E2B: &sandbox.E2BConfiguration{APIKey: "synthetic-team-a", Template: "public-team-b:" + uuid.NewString()}}
	if _, err := s.prepare(t.Context(), selection); !errors.Is(err, e2b.ErrTeamMismatch) || s.selected.Load() != nil {
		t.Fatal("public readability accepted as team ownership", err)
	}
}
