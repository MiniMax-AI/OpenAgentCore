package nativeinstaller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the shipped script against two actual TLS origins. The downloaded
// executable is a fixture so this test cannot install software or call a model.
func TestBootstrapDownloadsVerifiedPlatformAcrossHTTPSRedirect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bootstrap")
	}
	for _, tool := range []string{"bash", "curl", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OAC_BOOTSTRAP_TEST_RESULT\"\n")
	if err := tw.WriteHeader(&tar.Header{Name: "oac-daemon", Mode: 0700, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(archive.Bytes())
	platform := runtime.GOOS + "-" + runtime.GOARCH
	requests := 0
	artifact := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || strings.Contains(r.URL.String(), "private-grant") {
			t.Error("credential reached artifact origin")
		}
		if r.URL.Path != "/oac-native-build-"+platform+".tar.gz" {
			t.Error("wrong platform requested")
		}
		_, _ = w.Write(archive.Bytes())
	}))
	defer artifact.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: artifact.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			core := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".sha256") {
					checksum := hex.EncodeToString(hash[:])
					if !valid {
						checksum = strings.Repeat("0", 64)
					}
					fmt.Fprintln(w, checksum)
					return
				}
				if r.URL.Path != "/api/v1/agent-daemon/install/build/"+platform+".tar.gz" {
					t.Error("wrong request: " + r.URL.Path)
				}
				http.Redirect(w, r, artifact.URL+"/oac-native-build-"+platform+".tar.gz", http.StatusTemporaryRedirect)
			}))
			defer core.Close()
			result := filepath.Join(t.TempDir(), "result")
			command := exec.Command("bash", "assets/bootstrap.sh", core.URL+"/api/v1/agent-daemon/install/build", "private-grant", "--harness", "codex")
			command.Env = append(os.Environ(), "OAC_RUNTIME_HOME="+t.TempDir(), "CURL_CA_BUNDLE="+ca, "OAC_BOOTSTRAP_TEST_RESULT="+result, "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
			output, err := command.CombinedOutput()
			if valid {
				if err != nil {
					t.Fatalf("%v: %s", err, output)
				}
				args, err := os.ReadFile(result)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(args), core.URL+"/api/v1/agent-daemon/installation\n--authorization\nprivate-grant\n--harness\ncodex") {
					t.Fatalf("incorrect install handoff: %s", args)
				}
			} else {
				if err == nil || !strings.Contains(string(output), "checksum mismatch") {
					t.Fatalf("expected checksum failure: %v: %s", err, output)
				}
				if _, err := os.Stat(result); !os.IsNotExist(err) {
					t.Fatal("executed corrupt download")
				}
			}
			if strings.Contains(string(output), "private-grant") {
				t.Fatal("authorization in output")
			}
		})
	}
	if requests != 2 {
		t.Fatalf("got %d platform downloads", requests)
	}
}
