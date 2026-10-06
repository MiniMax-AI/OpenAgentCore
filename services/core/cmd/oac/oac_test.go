package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestApplyDoesNotStartWhenTheConfigurationCheckFails(t *testing.T) {
	var calls [][]string
	runner := scriptedRunner{run: func(args ...string) error {
		calls = append(calls, args)
		if args[0] == "run" {
			return os.ErrInvalid
		}
		return nil
	}}
	if err := apply(context.Background(), runner); err == nil {
		t.Fatal("apply continued")
	}
	if len(calls) != 1 || calls[0][0] != "run" {
		t.Fatal(calls)
	}
}

func TestRotateCoreKeyDigestDoesNotEchoTheKey(t *testing.T) {
	root := t.TempDir()
	in := installation{root: root, data: filepath.Join(root, "data")}
	if err := os.MkdirAll(filepath.Join(in.data, "secrets", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(in.data, "secrets", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.data, "secrets", "web", "core.key"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.data, "secrets", "core", "core-key-digests.json"), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var restarted bool
	runner := scriptedRunner{run: func(args ...string) error {
		restarted = args[0] == "restart"
		return nil
	}}
	if err := rotateCoreKey(context.Background(), in, runner); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(in.data, "secrets", "web", "core.key"))
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(raw))
	assertCoreKeyFormat(t, key)
	digest, err := os.ReadFile(filepath.Join(in.data, "secrets", "core", "core-key-digests.json"))
	if err != nil || !strings.Contains(string(digest), keyDigest(key)) || strings.Contains(string(digest), key) {
		t.Fatalf("digest %s key leaked %v", digest, err)
	}
	if !restarted {
		t.Fatal("core was not restarted")
	}
}

type scriptedRunner struct {
	run func(args ...string) error
}

func (s scriptedRunner) Run(_ context.Context, args ...string) error { return s.run(args...) }
func (s scriptedRunner) Output(context.Context, ...string) ([]byte, error) {
	return nil, nil
}

func assertCoreKeyFormat(t *testing.T, key string) {
	t.Helper()
	if !regexp.MustCompile(`^oac_admin_[0-9a-f]{64}$`).MatchString(key) {
		t.Fatal("core key must have the oac_admin_ prefix and 64 lowercase hexadecimal characters")
	}
}
