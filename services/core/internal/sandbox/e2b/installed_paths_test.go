package e2b

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestDirectProviderPreparesOnlyItsPrivateStateDirectory(t *testing.T) {
	artifacts := t.TempDir()
	binary, _, err := InstalledPaths(sandbox.ProcessPaths{ArtifactRoot: artifacts, StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("unused helper"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, shape := range []string{"fresh", "receipt", "file", "symlink", "public", "missing_parent"} {
		t.Run(shape, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "e2b")
			var err error
			switch shape {
			case "receipt":
				err = os.Mkdir(state, 0700)
				if err == nil {
					err = os.WriteFile(filepath.Join(state, "receipt.json"), []byte("retained receipt"), 0600)
				}
			case "file":
				err = os.WriteFile(state, []byte("retained file"), 0600)
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink creation requires Windows privileges")
				}
				err = os.Symlink(t.TempDir(), state)
			case "public":
				err = os.Mkdir(state, 0700)
				if err == nil {
					err = os.Chmod(state, 0755)
				}
			case "missing_parent":
				root = filepath.Join(root, "missing")
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.Lstat(state)
			_, err = BuildDirect(sandbox.DirectConfig{
				ProcessPaths:   sandbox.ProcessPaths{ArtifactRoot: artifacts, StateRoot: root},
				InstallationID: uuid.NewString(),
				Selection: sandbox.Selection{Provider: "e2b", Configuration: &DeploymentConfiguration{
					APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString(),
				}},
			})
			if (err == nil) != (shape == "fresh" || shape == "receipt") {
				t.Fatalf("unexpected construction result: %v", err)
			}
			after, statErr := os.Lstat(state)
			if shape == "fresh" && (statErr != nil || !after.IsDir() || after.Mode().Perm() != 0700) {
				t.Fatalf("fresh state is not private: %v %v", after, statErr)
			}
			if before != nil && (statErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode()) {
				t.Fatal("existing state was replaced or repaired")
			}
			if shape == "receipt" {
				data, err := os.ReadFile(filepath.Join(state, "receipt.json"))
				if err != nil || string(data) != "retained receipt" {
					t.Fatal("existing receipt changed", err)
				}
			}
			if shape == "missing_parent" {
				if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("adapter created the installation root", err)
				}
			}
		})
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
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"Version\":1,\"Templates\":[]}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	input := sandbox.ConfigurationDiscoveryInput{Credential: json.RawMessage(`{"api_key":"synthetic-key"}`)}
	result, err := (ConfigurationAdapter{}).DiscoverConfiguration(t.Context(), input, paths)
	if err != nil || string(result) != `{"templates":[]}` {
		t.Fatalf("configured discovery: %s %v", result, err)
	}
	if _, err := (ConfigurationAdapter{}).DiscoverConfiguration(t.Context(), input, sandbox.ProcessPaths{}); !errors.Is(err, sandbox.ErrConfigurationUnconfirmed) {
		t.Fatalf("missing roots: %v", err)
	}
}
