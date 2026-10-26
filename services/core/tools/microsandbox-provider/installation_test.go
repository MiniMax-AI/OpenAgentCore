//go:build linux

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIsolatedConfigReusesOnlyPrivateSnapshotBudget(t *testing.T) {
	home := t.TempDir()
	path, err := isolatedConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Snapshots struct {
			MaxFilesystemStateMiB int `json:"max_filesystem_state_mib"`
		} `json:"snapshots"`
	}
	if err := json.Unmarshal(data, &config); err != nil || config.Snapshots.MaxFilesystemStateMiB != 64 {
		t.Fatalf("snapshot budget: %s, %v", data, err)
	}
	if again, err := isolatedConfig(home); err != nil || again != path {
		t.Fatalf("reuse: %s %v", again, err)
	}
	if err := os.WriteFile(path, []byte(`{"default_profile":"other"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := isolatedConfig(home); err == nil {
		t.Fatal("ambient profile accepted")
	}
}

func TestIsolatedConfigRejectsChangedBudgetAndTrailingContent(t *testing.T) {
	for _, data := range []string{"{}\n", "{\"snapshots\":{\"max_filesystem_state_mib\":4095}}\n", "{\"snapshots\":{\"max_filesystem_state_mib\":64}}\n{}"} {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "oac-sdk-config.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := isolatedConfig(home); err == nil {
			t.Fatalf("accepted modified config: %q", data)
		}
	}
}

func TestIsolatedConfigRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, "oac-sdk-config.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := isolatedConfig(home); err == nil {
		t.Fatal("config symlink accepted")
	}
}
