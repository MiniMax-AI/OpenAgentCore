package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMicrosandboxProbeRequiresPrivateRuntimeDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("microsandbox requires Linux")
	}
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
			probe := microsandboxProbe(Microsandbox{HelperPath: artifact, RuntimePath: artifact, FirmwarePath: artifact, RuntimeSHA256: hex.EncodeToString(digest[:]), FirmwareSHA256: hex.EncodeToString(digest[:]), RuntimeHome: home})
			err := probe(t.Context())
			if tc.rejected {
				if err == nil || err.Error() != "microsandbox state directory is unavailable" {
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
					requireRuntimeDirectoryAccepted(t, probe(t.Context()))
				}
				return
			}
			requireRuntimeDirectoryAccepted(t, err)
		})
	}
}

func requireRuntimeDirectoryAccepted(t *testing.T, err error) {
	t.Helper()
	// The remaining host KVM check may fail in ordinary CI; directory validation
	// itself must pass without requiring a native runtime or creating a sandbox.
	if err != nil && err.Error() != "KVM is unavailable to sandbox node" {
		t.Fatal(err)
	}
}
