package e2b

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestInstalledPathsRejectMissingAndRelativeRoots(t *testing.T) {
	for _, paths := range []sandbox.ProcessPaths{{}, {ArtifactRoot: "/opt/oac"}, {StateRoot: "/state"}, {ArtifactRoot: "relative", StateRoot: "/state"}, {ArtifactRoot: "/opt/oac", StateRoot: "relative"}} {
		if _, _, err := InstalledPaths(paths); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatalf("invalid paths accepted: %v", err)
		}
	}
	binary, state, err := InstalledPaths(sandbox.ProcessPaths{ArtifactRoot: "/opt/oac", StateRoot: "/state"})
	if err != nil || binary != "/opt/oac/e2b/oac-e2b-provider" || state != "/state/e2b" {
		t.Fatalf("wrong paths: %s %s %v", binary, state, err)
	}
}

func TestConfigurationDiscoveryUsesSuppliedProcessPaths(t *testing.T) {
	paths := sandbox.ProcessPaths{ArtifactRoot: t.TempDir(), StateRoot: t.TempDir()}
	binary, _, err := InstalledPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"Version\":2,\"Templates\":[]}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	input := sandbox.ConfigurationDiscoveryInput{Credential: json.RawMessage(`{"api_key":"synthetic-key"}`)}
	result, err := (ConfigurationAdapter{}).DiscoverConfiguration(t.Context(), input, paths)
	if err != nil || len(result.Options) != 0 {
		t.Fatalf("configured discovery: %v %v", result, err)
	}
	if _, err := (ConfigurationAdapter{}).DiscoverConfiguration(t.Context(), input, sandbox.ProcessPaths{}); !errors.Is(err, sandbox.ErrConfigurationUnconfirmed) {
		t.Fatalf("missing roots: %v", err)
	}
}

func TestBuildDirectCreatesItsPrivateStateDirectory(t *testing.T) {
	paths := sandbox.ProcessPaths{ArtifactRoot: t.TempDir(), StateRoot: t.TempDir()}
	binary, state, err := InstalledPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	selection := sandbox.Selection{Provider: "e2b", Configuration: &DeploymentConfiguration{APIKey: "synthetic-key", Template: "test:" + uuid.NewString()}}
	for range 2 {
		if _, err := BuildDirect(sandbox.DirectConfig{ProcessPaths: paths, InstallationID: uuid.NewString(), Selection: selection}); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Stat(state); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("state directory: %v %v", info, err)
	}
}
