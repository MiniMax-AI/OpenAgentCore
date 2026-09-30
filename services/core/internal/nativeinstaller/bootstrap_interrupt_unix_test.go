//go:build unix

package nativeinstaller

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestBootstrapParentDeathKeepsActiveDownloadLocked(t *testing.T) {
	archive := bootstrapFixtureArchive(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			fmt.Fprintf(w, "%x\n", sha256.Sum256(archive))
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
		if downloads.Add(1) == 1 {
			w.Write(archive[:1])
			w.(http.Flusher).Flush()
			close(started)
			<-release
			return
		}
		w.Write(archive)
	}))
	defer server.Close()
	defer close(release)
	home := t.TempDir()
	command := bootstrapCommand(t, server.URL, home)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// Kill only this fixture's process group, even if an assertion fails.
	defer syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("download did not start")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	retry, err := bootstrapCommand(t, server.URL, home).CombinedOutput()
	if err == nil || !bytes.Contains(retry, []byte("Another native download")) {
		t.Fatalf("active download was not protected: %v %s", err, retry)
	}
	if _, err := os.Stat(filepath.Join(home, "native-download", "staging", "bundle.tar.gz")); err != nil {
		t.Fatal("active staging removed")
	}
	syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	_ = command.Wait()
	retry, err = bootstrapCommand(t, server.URL, home).CombinedOutput()
	if err != nil {
		t.Fatalf("recovery failed: %v %s", err, retry)
	}
	if _, err := os.Stat(filepath.Join(home, "native-download", "staging")); !os.IsNotExist(err) {
		t.Fatal("staging left after recovery")
	}
}

func TestBootstrapCommandDiscardsTimedOutResponse(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl unavailable")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Length", "1000")
			fmt.Fprint(w, "printf 'incomplete response")
			w.(http.Flusher).Flush()
			time.Sleep(700 * time.Millisecond)
			return
		}
		w.Write([]byte("printf '%s\\n' entry-success\n"))
	}))
	defer server.Close()
	// Shorten only the fixture's real curl timeout so the actual generated command is exercised.
	bin := t.TempDir()
	wrapper := "#!/bin/sh\nexec " + shellQuote(curl) + " \"$@\" --max-time 0.3\n"
	if err = os.WriteFile(filepath.Join(bin, "curl"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Version: "test"}
	command := exec.Command("bash", "-c", catalog.Commands(server.URL, "fixture-grant")["posix"])
	command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
	output, err := command.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("entry-success")) || bytes.Contains(output, []byte("incomplete response")) || requests.Load() != 2 {
		t.Fatalf("partial bootstrap executed: %v requests=%d %s", err, requests.Load(), output)
	}
}
