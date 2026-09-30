package providers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestMicrosandboxGenerationImageProbe(t *testing.T) {
	dir := t.TempDir()
	imageDigest := "sha256:" + strings.Repeat("d", 64)
	entry := Microsandbox{RuntimePath: filepath.Join(dir, "msb"), RuntimeHome: dir, FirmwarePath: filepath.Join(dir, "firmware"), Image: "oac-runtime@" + imageDigest}
	// Check the native argument/environment boundary, including ambient isolation.
	script := `#!/bin/sh
[ "$#" = 5 ] && [ "$1" = image ] && [ "$2" = inspect ] && [ "$3" = 'oac-runtime@` + imageDigest + `' ] && [ "$4" = --format ] && [ "$5" = json ] || exit 1
[ "$MSB_BACKEND" = local ] && [ "$MSB_PATH" = "$0" ] && [ "$MSB_LIBKRUNFW_PATH" = "$MSB_HOME/firmware" ] || exit 2
[ "$PATH" = /usr/local/bin:/usr/bin:/bin ] && [ -n "$HOME" ] && [ -z "${OAC_TEST_UNEXPECTED+x}" ] || exit 3
cat "$MSB_HOME/output"
`
	if err := os.WriteFile(entry.RuntimePath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_TEST_UNEXPECTED", "must not reach native command")
	t.Setenv("MSB_BACKEND", "ambient-backend")
	t.Setenv("HOME", dir)
	calls := 0
	probe := microsandboxGenerationProbe(entry, func(context.Context) error { calls++; return nil })
	valid := `{"Digest":"` + imageDigest + `","Architecture":"amd64","OS":"linux"}`
	for _, tc := range []struct {
		name, output string
		valid        bool
	}{
		{"ready", valid, true},
		{"invalid_json", `{`, false},
		{"trailing_json", valid + `{}`, false},
		{"wrong_digest", strings.Replace(valid, imageDigest, "sha256:"+strings.Repeat("e", 64), 1), false},
		{"wrong_architecture", strings.Replace(valid, "amd64", "arm64", 1), false},
		{"wrong_os", strings.Replace(valid, "linux", "darwin", 1), false},
		{"missing_digest", `{"Architecture":"amd64","OS":"linux"}`, false},
		{"oversize", valid + strings.Repeat(" ", 64*1024), false},
		{"recovered", valid, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "output"), []byte(tc.output), 0600); err != nil {
				t.Fatal(err)
			}
			err := probe(t.Context())
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, sandbox.ErrRuntimeImageUnavailable) {
				t.Fatalf("image result = %v", err)
			}
		})
	}
	if calls != 9 {
		t.Fatalf("base probe calls = %d", calls)
	}
	if err := os.Remove(filepath.Join(dir, "output")); err != nil {
		t.Fatal(err)
	}
	if err := probe(t.Context()); !errors.Is(err, sandbox.ErrRuntimeImageUnavailable) {
		t.Fatalf("command failure = %v", err)
	}
	if err := os.Remove(entry.RuntimePath); err != nil {
		t.Fatal(err)
	}
	if err := probe(t.Context()); !errors.Is(err, sandbox.ErrRuntimeImageUnavailable) {
		t.Fatalf("start failure = %v", err)
	}
}

func TestMicrosandboxGenerationProbeRetainsEarlierFailures(t *testing.T) {
	for _, failure := range []error{context.Canceled, sandbox.ErrKVMUnavailable, sandbox.ErrCapacityInsufficient, sandbox.ErrMicrosandboxArtifactsUnavailable, errors.New("microsandbox state directory is unavailable")} {
		probe := microsandboxGenerationProbe(Microsandbox{RuntimePath: "/missing"}, func(context.Context) error { return failure })
		if got := probe(t.Context()); got != failure {
			t.Fatalf("earlier failure changed: got %v, want %v", got, failure)
		}
	}
}
