package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestGenerationDroppedReceiptIdentity(t *testing.T) {
	config := sandbox.NodeConfig{InstallationID: "installation", Generation: 9, Provider: "docker"}
	stateDir := t.TempDir()
	directory := filepath.Join(stateDir, "generations")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "9.dropped")
	journal := map[string]any{"installation_id": config.InstallationID, "generation": config.Generation, "specification_digest": config.Specification.Digest(config.Provider)}
	write := func(value map[string]any) {
		raw, _ := json.Marshal(value)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if dropped, err := generationDropped(config, stateDir); err != nil || dropped {
		t.Fatal("absent receipt", dropped, err)
	}
	write(journal)
	if dropped, err := generationDropped(config, stateDir); err != nil || !dropped {
		t.Fatal(dropped, err)
	}
	for _, field := range []string{"installation_id", "generation", "specification_digest"} {
		previous := journal[field]
		journal[field] = "foreign"
		write(journal)
		if _, err := generationDropped(config, stateDir); err == nil {
			t.Fatal("accepted mismatched", field)
		}
		journal[field] = previous
	}
	journal["native_complete"] = true
	write(journal)
	if _, err := generationDropped(config, stateDir); err == nil {
		t.Fatal("accepted an unknown journal field")
	}
	delete(journal, "native_complete")
	write(journal)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := generationDropped(config, stateDir); err == nil {
		t.Fatal("accepted non-private marker")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(directory, "foreign")
	raw, _ := json.Marshal(journal)
	if err := os.WriteFile(foreign, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []func(string, string) error{os.Symlink, os.Link} {
		if err := link(foreign, path); err != nil {
			t.Fatal(err)
		}
		if _, err := generationDropped(config, stateDir); err == nil {
			t.Fatal("accepted linked journal")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := generationDropped(config, stateDir); err == nil {
		t.Fatal("accepted FIFO journal")
	}
}

func TestGenerationHelperKeepsCancellation(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 1").Run()
	if got := generationHelperError(t.Context(), err); got == nil || errors.Is(got, context.Canceled) {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(generationHelperError(ctx, err), context.Canceled) {
		t.Fatal("cancellation was lost")
	}
}

// Preparation may resolve native state, such as the digest a containerd image
// store names the loaded Runtime image by; the envelope stays fixed.
func TestGenerationPlanFixesOnlyTheEnvelope(t *testing.T) {
	plan := sandbox.NodeConfig{InstallationID: "installation", Generation: 2, Provider: "docker", Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}, Native: json.RawMessage(`{"image":"sha256:` + strings.Repeat("b", 64) + `"}`)}
	final := plan
	final.Native = json.RawMessage(`{"image":"sha256:` + strings.Repeat("c", 64) + `"}`)
	if !sameGenerationPlan(final, plan) {
		t.Fatal("refused a resolved native image")
	}
	final.Specification.Resources.CPUs = 4
	if sameGenerationPlan(final, plan) {
		t.Fatal("accepted a different specification")
	}
}

func TestGenerationStateDirectoryOwnership(t *testing.T) {
	for _, mode := range []string{"private", "public", "symlink", "symlink_parent"} {
		t.Run(mode, func(t *testing.T) {
			stateDir := t.TempDir()
			directory := filepath.Join(stateDir, "generations")
			if mode == "symlink" {
				if err := os.Symlink(t.TempDir(), directory); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "public" {
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "symlink_parent" {
				link := filepath.Join(t.TempDir(), "node")
				if err := os.Symlink(stateDir, link); err != nil {
					t.Fatal(err)
				}
				stateDir = link
			}
			_, err := generationDropped(sandbox.NodeConfig{Generation: 1}, stateDir)
			if mode == "private" && err != nil || mode != "private" && !errors.Is(err, sandbox.ErrOwnership) {
				t.Fatalf("directory ownership: %v", err)
			}
		})
	}
}
