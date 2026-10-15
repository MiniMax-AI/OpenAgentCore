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
	"testing"
	"time"
)

// resumeServer serves the installation endpoint and a ranged archive.
type resumeServer struct {
	*httptest.Server
	archive            []byte
	downloads, checks  atomic.Int32
	ranges             chan string
	grantStatus        int
	stallFirstDownload bool
}

func newResumeServer(t *testing.T, archive []byte) *resumeServer {
	t.Helper()
	s := &resumeServer{archive: archive, ranges: make(chan string, 8), grantStatus: 200}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/installation"):
			s.checks.Add(1)
			if r.Header.Get("Authorization") != "Bearer fixture-grant" {
				t.Error("grant missing from the installation check")
			}
			w.WriteHeader(s.grantStatus)
			fmt.Fprint(w, `{"harness":"codex"}`)
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			fmt.Fprintf(w, "%s\n", sha256Hex(archive))
		default:
			request := s.downloads.Add(1)
			s.ranges <- r.Header.Get("Range")
			if s.stallFirstDownload && request == 1 {
				w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
				w.Write(archive[:len(archive)/2])
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(archive))
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *resumeServer) command(t *testing.T, home string, args ...string) *exec.Cmd {
	command := exec.Command("bash", append([]string{"assets/bootstrap.sh", s.URL + "/api/v1/agent-daemon/install/test"}, args...)...)
	command.Env = append(os.Environ(), "OAC_RUNTIME_HOME="+home, GrantEnv+"=fixture-grant", "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
	return command
}

func TestBootstrapResumesStalledDownload(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl unavailable")
	}
	archive := bootstrapFixtureArchive(t)
	server := newResumeServer(t, archive)
	server.stallFirstDownload = true
	// Shorten only the stall window; the script's own retry and resume logic runs.
	bin := t.TempDir()
	if err = os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\nexec "+shellQuote(curl)+" \"$@\" --speed-time 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := server.command(t, t.TempDir())
	command.Env = append(command.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if first, second := <-server.ranges, <-server.ranges; first != "" || !strings.HasPrefix(second, "bytes=") {
		t.Fatalf("second request did not resume: %q then %q", first, second)
	}
}

func TestBootstrapKeepsVerifiedArchiveUntilInstallCompletes(t *testing.T) {
	// The first install attempt fails after the download; the rerun needs no new archive and removes it on success.
	archive := bootstrapScriptArchive(t, "#!/bin/sh\n[ -e \"$OAC_RESUME_OK\" ]\n")
	server := newResumeServer(t, archive)
	home, ok := t.TempDir(), filepath.Join(t.TempDir(), "ok")
	cached := filepath.Join(home, "native-download", sha256Hex(archive)+".tar.gz")
	command := server.command(t, home)
	command.Env = append(command.Env, "OAC_RESUME_OK="+ok)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("expected the fixture install to fail: %s", output)
	}
	if _, err := os.Stat(cached); err != nil {
		t.Fatal("verified archive was not kept")
	}
	if err := os.WriteFile(ok, nil, 0600); err != nil {
		t.Fatal(err)
	}
	command = server.command(t, home)
	command.Env = append(command.Env, "OAC_RESUME_OK="+ok)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if server.downloads.Load() != 1 {
		t.Fatalf("archive downloaded %d times", server.downloads.Load())
	}
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Fatal("archive kept after a completed installation")
	}
}

func TestBootstrapDiscardsCachedArchiveThatFailsVerification(t *testing.T) {
	archive := bootstrapFixtureArchive(t)
	server := newResumeServer(t, archive)
	home := t.TempDir()
	cached := filepath.Join(home, "native-download", sha256Hex(archive)+".tar.gz")
	if err := os.MkdirAll(filepath.Dir(cached), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := server.command(t, home).CombinedOutput(); err != nil || server.downloads.Load() != 1 {
		t.Fatalf("%v downloads=%d: %s", err, server.downloads.Load(), output)
	}
}

func TestBootstrapChecksGrantBeforeDownloading(t *testing.T) {
	server := newResumeServer(t, bootstrapFixtureArchive(t))
	server.grantStatus = 401
	output, err := server.command(t, t.TempDir()).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "authorization rejected or expired") || server.downloads.Load() != 0 || strings.Contains(string(output), "fixture-grant") {
		t.Fatalf("%v downloads=%d: %s", err, server.downloads.Load(), output)
	}
}

func sha256Hex(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
