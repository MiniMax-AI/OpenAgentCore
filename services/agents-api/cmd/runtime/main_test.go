package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeCredentialRequiresPrivateOwnedRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credential.json")
	if err := os.WriteFile(path, []byte(`{"executor_token":"synthetic"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateCredential(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateCredential(path); err == nil {
		t.Fatal("public credential accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateCredential(alias); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateCredential(path); err == nil {
		t.Fatal("hardlink accepted")
	}
}
