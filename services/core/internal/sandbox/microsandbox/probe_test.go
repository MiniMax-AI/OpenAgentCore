package microsandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

var smallSandbox = sandbox.Resources{CPUs: 1, MemoryMiB: 512}

// useKVM points the probe at a readable/writable stand-in, or at a missing path.
func useKVM(t *testing.T, available bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kvm")
	if available {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	previous := kvmDevice
	kvmDevice = path
	t.Cleanup(func() { kvmDevice = previous })
}

func TestMicrosandboxProbeRequiresPrivateRuntimeDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("microsandbox requires Linux")
	}
	useKVM(t, true)
	for _, tc := range []struct {
		name     string
		mode     os.FileMode
		symlink  bool
		rejected bool
	}{
		{name: "private", mode: 0700},
		{name: "world_readable", mode: 0755, rejected: true},
		{name: "symlink", mode: 0700, symlink: true, rejected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			artifact := filepath.Join(dir, "artifact")
			content := []byte("pinned probe fixture")
			if err := os.WriteFile(artifact, content, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(artifact, 0700); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(content)
			home := filepath.Join(dir, "runtime")
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(home, tc.mode); err != nil {
				t.Fatal(err)
			}
			if tc.symlink {
				link := filepath.Join(dir, "runtime-link")
				if err := os.Symlink(home, link); err != nil {
					t.Fatal(err)
				}
				home = link
			}
			probe := microsandboxProbe(Config{HelperPath: artifact, RuntimePath: artifact, FirmwarePath: artifact, RuntimeSHA256: hex.EncodeToString(digest[:]), FirmwareSHA256: hex.EncodeToString(digest[:]), RuntimeHome: home}, smallSandbox)
			err := probe(t.Context())
			if tc.rejected {
				// An unclassified cause keeps the generic readiness code.
				if err == nil || err.Error() != "microsandbox state directory is unavailable" || sandbox.NodeDiagnostic(err) != "provider_unavailable" {
					t.Fatalf("unsafe runtime directory accepted: %v", err)
				}
				state, statErr := os.Lstat(home)
				if statErr != nil {
					t.Fatal(statErr)
				}
				if tc.symlink {
					if state.Mode()&os.ModeSymlink == 0 {
						t.Fatal("probe replaced runtime symlink")
					}
				} else {
					if state.Mode().Perm() != tc.mode {
						t.Fatal("probe changed runtime permissions")
					}
					if err := os.Chmod(home, 0700); err != nil {
						t.Fatal(err)
					}
					if err := probe(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMicrosandboxProbeDiagnostics(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("microsandbox requires Linux")
	}
	dir := t.TempDir()
	missing := Config{HelperPath: filepath.Join(dir, "helper"), RuntimePath: filepath.Join(dir, "runtime"), FirmwarePath: filepath.Join(dir, "firmware"), RuntimeSHA256: strings.Repeat("a", 64), FirmwareSHA256: strings.Repeat("b", 64), RuntimeHome: dir}
	for _, tc := range []struct {
		name      string
		kvm       bool
		resources sandbox.Resources
		want      string
	}{
		// KVM is reported before capacity and missing artifacts.
		{name: "kvm", resources: sandbox.Resources{CPUs: 255, MemoryMiB: 1048576}, want: "host_unsupported"},
		{name: "capacity", kvm: true, resources: sandbox.Resources{CPUs: 255, MemoryMiB: 1048576}, want: "capacity_insufficient"},
		{name: "artifacts", kvm: true, resources: smallSandbox, want: "artifacts_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useKVM(t, tc.kvm)
			if got := sandbox.NodeDiagnostic(microsandboxProbe(missing, tc.resources)(t.Context())); got != tc.want {
				t.Fatalf("diagnostic = %q, want %q", got, tc.want)
			}
		})
	}
}

// A failed integrity check is repeated, so repaired artifacts recover without a restart.
func TestMicrosandboxProbeRecoversRepairedArtifacts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("microsandbox requires Linux")
	}
	useKVM(t, true)
	dir := t.TempDir()
	content := []byte("runtime")
	digest := sha256.Sum256(content)
	artifact, home := filepath.Join(dir, "artifact"), filepath.Join(dir, "runtime")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	probe := microsandboxProbe(Config{HelperPath: artifact, RuntimePath: artifact, FirmwarePath: artifact, RuntimeSHA256: hex.EncodeToString(digest[:]), FirmwareSHA256: hex.EncodeToString(digest[:]), RuntimeHome: home}, smallSandbox)
	if got := sandbox.NodeDiagnostic(probe(t.Context())); got != "artifacts_unavailable" {
		t.Fatalf("missing artifact diagnostic = %q", got)
	}
	if err := os.WriteFile(artifact, content, 0700); err != nil {
		t.Fatal(err)
	}
	if err := probe(t.Context()); err != nil {
		t.Fatalf("repaired artifact stayed unavailable: %v", err)
	}
}
