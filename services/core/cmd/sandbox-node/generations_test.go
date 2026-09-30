package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	providerconfig "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

func TestGenerationJournalRestartIdentity(t *testing.T) {
	config := providerconfig.Config{InstallationID: "installation", Generation: 9, Provider: "docker"}
	stateDir := t.TempDir()
	directory := filepath.Join(stateDir, "generations")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	journal := map[string]any{"installation_id": config.InstallationID, "generation": config.Generation, "specification_digest": config.Specification.Digest(config.Provider)}
	for _, suffix := range []string{".collecting", ".dropped"} {
		t.Run(suffix, func(t *testing.T) {
			path := filepath.Join(directory, "9"+suffix)
			if suffix == ".collecting" {
				journal["native_complete"] = true
			} else {
				delete(journal, "native_complete")
			}
			write := func(value map[string]any) {
				raw, _ := json.Marshal(value)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(journal)
			state, err := generationLocalState(config, stateDir)
			if err != nil || state != suffix[1:] {
				t.Fatal(state, err)
			}
			for _, field := range []string{"installation_id", "generation", "specification_digest"} {
				previous := journal[field]
				journal[field] = "foreign"
				write(journal)
				if _, err = generationLocalState(config, stateDir); err == nil {
					t.Fatal("accepted mismatched", field)
				}
				journal[field] = previous
			}
			journal["native_complete"] = nil
			write(journal)
			if _, err = generationLocalState(config, stateDir); err == nil {
				t.Fatal("accepted null journal phase")
			}
			if suffix == ".collecting" {
				journal["native_complete"] = true
			} else {
				delete(journal, "native_complete")
			}
			write(journal)
			if err = os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err = generationLocalState(config, stateDir); err == nil {
				t.Fatal("accepted non-private marker")
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(directory, "foreign")
			raw, _ := json.Marshal(journal)
			if err = os.WriteFile(foreign, raw, 0600); err != nil {
				t.Fatal(err)
			}
			for _, link := range []func(string, string) error{os.Symlink, os.Link} {
				if err = link(foreign, path); err != nil {
					t.Fatal(err)
				}
				if _, err = generationLocalState(config, stateDir); err == nil {
					t.Fatal("accepted linked journal")
				}
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err = syscall.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = generationLocalState(config, stateDir); err == nil {
				t.Fatal("accepted FIFO journal")
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGenerationHelperUsesOnlyTypedExit(t *testing.T) {
	for _, code := range []string{"1", "65", "78"} {
		err := exec.Command("sh", "-c", "exit "+code).Run()
		got := generationHelperError(t.Context(), err)
		if errors.Is(got, sandbox.ErrRuntimeDownloadFailed) != (code == "65") {
			t.Fatal(code, got)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if !errors.Is(generationHelperError(ctx, err), context.Canceled) {
			t.Fatal("cancellation was lost")
		}
	}
	if errors.Is(generationHelperError(t.Context(), errors.New("runtime_download_failed")), sandbox.ErrRuntimeDownloadFailed) {
		t.Fatal("parsed arbitrary provider text")
	}
}

func TestUnresolvedPreparationRemainsRecoveryOnly(t *testing.T) {
	config := providerconfig.Config{InstallationID: "installation", Generation: 2, Provider: "docker"}
	stateDir := t.TempDir()
	directory := filepath.Join(stateDir, "generations")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "2.preparing")
	for _, started := range []bool{false, true} {
		raw, _ := json.Marshal(map[string]any{"installation_id": config.InstallationID, "generation": config.Generation, "specification_digest": config.Specification.Digest(config.Provider), "import_started": started, "configuration": config})
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		journal, err := readGenerationJournal(path)
		if err != nil || journal.Configuration == nil {
			t.Fatal("pending-only discovery", err)
		}
		state, err := generationLocalState(*journal.Configuration, stateDir)
		if err != nil || state != "preparing" {
			t.Fatal(state, err)
		}
		if _, err := os.Stat(filepath.Join(directory, "2.json")); !os.IsNotExist(err) {
			t.Fatal("unresolved plan published")
		}
	}
}
