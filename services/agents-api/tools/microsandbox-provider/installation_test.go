//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsolatedConfigReusesOnlyPrivateEmptyObject(t *testing.T) {
	home := t.TempDir()
	path, err := isolatedConfig(home)
	if err != nil {
		t.Fatal(err)
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

func TestIsolatedConfigRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, "parsar-sdk-config.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := isolatedConfig(home); err == nil {
		t.Fatal("config symlink accepted")
	}
}
