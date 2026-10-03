package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/google/uuid"
)

func TestE2BRejectedSpecificationHasSafeActionableDiagnostic(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "provider")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"Version\":"+strconv.Itoa(e2b.ProtocolVersion)+",\"ErrorCode\":\"invalid\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "e2b")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	paths := testProviderPaths(t, helper, state)
	id := uuid.NewString()
	s := &managedSetup{capacity: testSandboxCapacity(t), processPaths: paths, registry: providers.Builtin(), installationID: id}
	selection := deployment.Setup{InstallationID: id, Provider: "e2b", Mode: "direct", UsesCredential: true, Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 3, MemoryMiB: 3072}}, Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-private-key", Template: "runtime:" + uuid.NewString()}}
	_, err := s.prepare(t.Context(), selection)
	if !errors.Is(err, sandbox.ErrConfigurationSelection) || strings.Contains(err.Error(), "synthetic-private-key") || s.selected.Load() != nil {
		t.Fatal("rejected candidate lost its safe diagnostic or was published", err)
	}
	s.deployment = &fakeDeploymentSetups{t: t, setup: committedSetup(&selection)}
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
    print(json.dumps({'Version': q['Version'], 'ErrorCode': 'invalid'}))
else:
    info = dict(q['Reference'], ProviderID='owned-compute', State='stopped', CreateSettled=True)
    if op == 'kill':
        info.update(ProviderID='', State='absent')
    print(json.dumps({'Version': q['Version'], 'Info': info}))
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "e2b")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	paths := testProviderPaths(t, helper, state)
	id := uuid.NewString()
	selection := deployment.Setup{InstallationID: id, Provider: "e2b", Mode: "direct", Generation: 1, UsesCredential: true,
		Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}},
		Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-private-key", Template: "runtime:" + uuid.NewString()}}
	allocation := func(context.Context, sandbox.Reference) (deployment.Setup, error) { return selection, nil }
	s := &managedSetup{capacity: testSandboxCapacity(t), processPaths: paths, registry: providers.Builtin(), installationID: id,
		deployment: &fakeDeploymentSetups{t: t, setup: committedSetup(&selection), allocationSetup: allocation}}
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
	state := filepath.Join(t.TempDir(), "e2b")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "provider")
	requests := filepath.Join(state, "requests")
	script := "#!/bin/sh\ncat >>" + requests + "\necho >>" + requests + "\nprintf '%s' '{\"Version\":" + strconv.Itoa(e2b.ProtocolVersion) + ",\"ErrorCode\":\"\",\"DeploymentValid\":true,\"TemplateBuild\":{\"Status\":\"ready\",\"CPUs\":4,\"MemoryMiB\":4096,\"RootDiskMiB\":24063}}'\n"
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	paths := testProviderPaths(t, helper, state)
	id := uuid.NewString()
	s := &managedSetup{capacity: testSandboxCapacity(t), processPaths: paths, registry: providers.Builtin(), installationID: id, deployment: &fakeDeploymentSetups{t: t}}
	selection := deployment.Setup{InstallationID: id, Provider: "e2b", Mode: "direct", UsesCredential: true, Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-private-key", Template: "runtime:" + uuid.NewString()}}
	candidate, err := s.prepare(t.Context(), selection)
	disk := int32(24063)
	if err != nil || candidate.Selection.Configuration.(*e2b.DeploymentConfiguration).TemplateBuild == nil || candidate.Selection.Configuration.(*e2b.DeploymentConfiguration).TemplateBuild.CPUs != 4 || candidate.Selection.Configuration.(*e2b.DeploymentConfiguration).TemplateBuild.MemoryMiB != 4096 ||
		*candidate.Selection.Configuration.(*e2b.DeploymentConfiguration).TemplateBuild.RootDiskMiB != disk || candidate.Selection.Configuration.(*e2b.DeploymentConfiguration).TemplateBuild.Status != "ready" {
		t.Fatalf("validated build was not recorded: %+v %v", candidate.Selection.Configuration.(*e2b.DeploymentConfiguration).TemplateBuild, err)
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
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"Version\":"+strconv.Itoa(e2b.ProtocolVersion)+",\"ErrorCode\":\"team_mismatch\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "e2b")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	paths := testProviderPaths(t, helper, state)
	id := uuid.NewString()
	s := &managedSetup{capacity: testSandboxCapacity(t), processPaths: paths, registry: providers.Builtin(), installationID: id, deployment: &fakeDeploymentSetups{t: t}}
	selection := deployment.Setup{InstallationID: id, Provider: "e2b", Mode: "direct", UsesCredential: true, Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-team-a", Template: "public-team-b:" + uuid.NewString()}}
	if _, err := s.prepare(t.Context(), selection); !errors.Is(err, sandbox.ErrCredentialOwnership) || s.selected.Load() != nil {
		t.Fatal("public readability accepted as team ownership", err)
	}
}

func TestManagedSetupRoutesProviderWithoutCredentialRequirement(t *testing.T) {
	s := &managedSetup{capacity: testSandboxCapacity(t)}
	config := &execution.RuntimeProvider{ProviderKind: "docker"}
	candidate, err := s.routeGenerations(
		execution.PreparedRuntimeDeployment{Config: config},
		deployment.Setup{Provider: "docker", Mode: "nodes"},
	)
	if err != nil || candidate.Config != config || candidate.FenceCredential != nil || candidate.VerifyCredential != nil {
		t.Fatalf("explicit no-credential provider required credential routing: %v", err)
	}
}
