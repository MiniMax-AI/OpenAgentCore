package nativeinstaller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func bootstrapFixtureArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	z := gzip.NewWriter(&buffer)
	tarball := tar.NewWriter(z)
	data := []byte("#!/bin/sh\nexit 0\n")
	if err := tarball.WriteHeader(&tar.Header{Name: "oac-daemon", Mode: 0700, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarball.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func bootstrapCommand(t *testing.T, base, home string) *exec.Cmd {
	t.Helper()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-File", "assets/bootstrap.ps1", "-Base", base, "-Authorization", "fixture-grant")
	} else {
		command = exec.Command("bash", "assets/bootstrap.sh", base, "fixture-grant")
	}
	command.Env = append(os.Environ(), "OAC_RUNTIME_HOME="+home, "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
	return command
}

func TestBootstrapRecovery(t *testing.T) {
	archive := bootstrapFixtureArchive(t)
	for _, scenario := range []string{"metadata-503", "archive-truncated", "missing", "corrupt"} {
		t.Run(scenario, func(t *testing.T) {
			var metadata, downloads atomic.Int32
			sum := fmt.Sprintf("%x", sha256.Sum256(archive))
			// Windows exercises the real downloader, then rejects the fixture before execution.
			if scenario == "corrupt" || runtime.GOOS == "windows" {
				sum = strings.Repeat("0", 64)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".sha256") {
					request := metadata.Add(1)
					if scenario == "missing" {
						w.WriteHeader(404)
						return
					}
					if scenario == "metadata-503" && request == 1 {
						w.WriteHeader(503)
						return
					}
					fmt.Fprintln(w, sum)
					return
				}
				request := downloads.Add(1)
				w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
				if scenario == "archive-truncated" && request == 1 {
					w.Write(archive[:len(archive)/2])
					return
				}
				w.Write(archive)
			}))
			defer server.Close()
			home := filepath.Join(t.TempDir(), "Runtime home with spaces")
			output, err := bootstrapCommand(t, server.URL+"/api/v1/agent-daemon/install/test", home).CombinedOutput()
			if scenario == "missing" {
				if err == nil || !bytes.Contains(output, []byte("no qualified installer")) || downloads.Load() != 0 || metadata.Load() != 1 {
					t.Fatalf("missing: %v %s", err, output)
				}
			} else if scenario == "corrupt" || runtime.GOOS == "windows" {
				if err == nil || !bytes.Contains(output, []byte("checksum mismatch")) {
					t.Fatalf("corrupt: %v %s", err, output)
				}
			} else if err != nil {
				t.Fatalf("recovery failed: %v %s", err, output)
			}
			if scenario == "metadata-503" && metadata.Load() != 2 {
				t.Fatalf("metadata requests=%d", metadata.Load())
			}
			if scenario == "archive-truncated" && downloads.Load() != 2 {
				t.Fatalf("download requests=%d", downloads.Load())
			}
			if _, err := os.Stat(filepath.Join(home, "native-download", "staging")); !os.IsNotExist(err) {
				t.Fatalf("left staging: %v", err)
			}
			if bytes.Contains(output, []byte("fixture-grant")) || bytes.Contains(output, []byte("\x1b")) {
				t.Fatal("secret or terminal escape in captured output")
			}
		})
	}
}

func TestBootstrapDiscardsStaleStagingOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture; Windows recovery is covered separately")
	}
	home := t.TempDir()
	staging := filepath.Join(home, "native-download", "staging")
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "interrupted"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(home, "workspace.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer server.Close()
	if output, err := bootstrapCommand(t, server.URL, home).CombinedOutput(); err == nil {
		t.Fatalf("unexpected success %s", output)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatal("stale staging remains")
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "keep" {
		t.Fatal("unrelated file changed")
	}
}

func TestBootstrapRejectsLowSpaceBeforeNetwork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX disk-space fixture")
	}
	home := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "df"), []byte("#!/bin/sh\nprintf 'Filesystem 1024-blocks Used Available Capacity Mounted\\nfixture 100 99 1 99%% /\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	command := bootstrapCommand(t, server.URL, home)
	command.Env = append(command.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("Not enough disk space")) || requests.Load() != 0 {
		t.Fatalf("low-space guard: %v %s requests=%d", err, output, requests.Load())
	}
	if _, err = os.Stat(filepath.Join(home, "native-download", "staging")); !os.IsNotExist(err) {
		t.Fatal("low-space failure left staging")
	}
}
