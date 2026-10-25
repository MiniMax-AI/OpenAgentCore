package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/machineapi"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// fakeCore serves the installation endpoints and one archive per component.
type fakeCore struct {
	*httptest.Server
	mu       sync.Mutex
	archives map[string][]byte
	bodies   map[string]int // archive downloads by component
	breakOn  string         // component whose first download is cut off halfway
	status   int            // answer for /installation
	redirect string         // when set, archives redirect here
}

func newFakeCore(t *testing.T, workspace string) (*fakeCore, string) {
	t.Helper()
	environment := uuid.NewString()
	core := &fakeCore{archives: map[string][]byte{}, bodies: map[string]int{}, status: 200}
	core.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		core.mu.Lock()
		defer core.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/installation/claim"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/installation"):
			if core.status != 200 {
				w.WriteHeader(core.status)
				return
			}
			_ = json.NewEncoder(w).Encode(machineapi.InstallationContext{Version: Version, ProtocolVersion: proto.Version, EnvironmentID: environment,
				RemoteURL: strings.Replace(core.URL, "http://", "ws://", 1) + "/api/v1/agent-daemon/ws", Workspace: workspace, Harness: nativeHarnesses["codex"].AgentKind})
		default:
			name, ext, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/v1/agent-daemon/install/"+Version+"/"+runtime.GOOS+"-"+runtime.GOARCH+"-"), ".")
			data, ok := core.archives[name]
			switch {
			case !ok:
				w.WriteHeader(http.StatusNotFound)
			case ext == "sha256":
				fmt.Fprintf(w, "%x\n", sha256.Sum256(data))
			case core.redirect != "":
				http.Redirect(w, r, core.redirect, http.StatusTemporaryRedirect)
			case name == core.breakOn && r.Header.Get("Range") == "":
				core.bodies[name]++
				w.Header().Set("Content-Length", fmt.Sprint(len(data)))
				_, _ = w.Write(data[:len(data)/2])
				w.(http.Flusher).Flush()
				panic(http.ErrAbortHandler)
			default:
				if r.Header.Get("Range") == "" {
					core.bodies[name]++
				}
				http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
			}
		}
	}))
	t.Cleanup(core.Close)
	return core, environment
}

// add publishes a component archive and returns its bundle.json entry.
func (c *fakeCore) add(t *testing.T, name string) nativeComponent {
	t.Helper()
	data := []byte("fixture " + name + strings.Repeat(" padding", 4096))
	sum := sha256.Sum256(data)
	var archive bytes.Buffer
	zipped := gzip.NewWriter(&archive)
	writer := tar.NewWriter(zipped)
	for _, header := range []*tar.Header{{Name: "components/" + name + "/", Typeflag: tar.TypeDir, Mode: 0755}, {Name: "components/" + name + "/program", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(data))}} {
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = writer.Write(data)
	if writer.Close() != nil || zipped.Close() != nil {
		t.Fatal("cannot build archive")
	}
	c.archives[name] = archive.Bytes()
	return nativeComponent{Version: nativePins[name], Files: map[string]nativeFile{"program": {SHA256: hex.EncodeToString(sum[:]), Executable: true}}}
}

func nativeDownloadInstall(t *testing.T, core *fakeCore, bundle string, extra ...string) error {
	t.Helper()
	t.Setenv(machineapi.InstallGrantEnv, "grant")
	out := new(bytes.Buffer)
	rc := &runContext{stdin: strings.NewReader(""), stdout: out, stderr: out}
	o, err := parseNativeInstall(rc, append([]string{"--non-interactive", "--onboard-url", core.URL + "/api/v1/agent-daemon/installation", "--bundle-dir", bundle, "--harness", "codex"}, extra...))
	if err != nil {
		return err
	}
	return installNativeOptions(context.Background(), rc, &o)
}

func writeBundle(t *testing.T, bundle string, components map[string]nativeComponent) {
	t.Helper()
	raw, _ := json.Marshal(nativeBundle{Schema: 1, DaemonVersion: Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Components: components})
	if err := os.WriteFile(filepath.Join(bundle, "bundle.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeInstallDownloadsOnlySelectedComponentsOnceAndResumes(t *testing.T) {
	home := nativeTestHome(t)
	oldDelay, oldProbe := nativeRetryDelay, probeNativeInstallation
	nativeRetryDelay = time.Millisecond
	probeNativeInstallation = func(context.Context, string, []string) error { return nil }
	t.Cleanup(func() { nativeRetryDelay, probeNativeInstallation = oldDelay, oldProbe })
	core, _ := newFakeCore(t, t.TempDir())
	components := map[string]nativeComponent{}
	for _, name := range []string{"node", "codex", "claude"} {
		components[name] = core.add(t, name)
	}
	core.breakOn = "codex"
	cache := filepath.Join(home, "native-download")
	if err := os.MkdirAll(filepath.Join(cache, "older-version"), 0700); err != nil {
		t.Fatal(err)
	}
	// Two Sessions of one release on one machine download each component once, into one store.
	var installations []string
	for i := 0; i < 2; i++ {
		bundle, installation := t.TempDir(), t.TempDir()
		installations = append(installations, installation)
		writeBundle(t, bundle, components)
		if err := nativeDownloadInstall(t, core, bundle, "--install-dir", installation); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(bundle, "components")); err == nil {
			t.Fatal("extracted into the distribution directory")
		}
		if _, err := os.Stat(filepath.Join(installation, "components")); err == nil {
			t.Fatal("copied components into the installation")
		}
	}
	if core.bodies["node"] != 1 || core.bodies["claude"] != 0 {
		t.Fatalf("unexpected downloads: %v", core.bodies)
	}
	if core.bodies["codex"] != 1 {
		t.Fatalf("codex was fetched %d times, want its interrupted download resumed", core.bodies["codex"])
	}
	store := nativeTestStore(t)
	for _, name := range []string{"node", "codex"} {
		if err := verifyNativeComponent(context.Background(), filepath.Join(store, name), name, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(store, "claude")); err == nil {
		t.Fatal("downloaded a Harness the Sessions do not use")
	}
	if left, _ := filepath.Glob(filepath.Join(cache, Version, "*")); len(left) != 0 {
		t.Fatalf("archives remain after extraction: %v", left)
	}
	if _, err := os.Stat(filepath.Join(cache, "older-version")); err == nil {
		t.Fatal("downloads of another version were kept")
	}
}

func TestNativeInstallRejectsGrantBeforeDownloading(t *testing.T) {
	nativeTestHome(t)
	core, _ := newFakeCore(t, t.TempDir())
	core.status = http.StatusUnauthorized
	if err := nativeDownloadInstall(t, core, t.TempDir()); err == nil || !strings.Contains(err.Error(), "authorization rejected") {
		t.Fatalf("expected a rejected grant, got %v", err)
	}
	if len(core.bodies) != 0 {
		t.Fatal("downloaded after a rejected grant")
	}
}

func TestNativeInstallRefusesRedirectToHTTP(t *testing.T) {
	nativeTestHome(t)
	core, _ := newFakeCore(t, t.TempDir())
	components := map[string]nativeComponent{"node": core.add(t, "node"), "codex": core.add(t, "codex")}
	core.redirect = "http://127.0.0.1:1/archive.tar.gz"
	bundle := t.TempDir()
	writeBundle(t, bundle, components)
	if err := nativeDownloadInstall(t, core, bundle, "--install-dir", t.TempDir()); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected an HTTPS-only redirect error, got %v", err)
	}
}

func TestNativeInstallFromDistributionNamesMissingArchive(t *testing.T) {
	rc, args, _, bundle := nativeInstallFixture(t)
	if err := os.RemoveAll(bundleComponent(bundle, "codex")); err != nil {
		t.Fatal(err)
	}
	err := runInstall(rc, args)
	want := fmt.Sprintf("extract %s-%s-codex.tar.gz", runtime.GOOS, runtime.GOARCH)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected an error naming %s, got %v", want, err)
	}
}

func TestNativeDownloadFailsClearlyWhenCoreStopsAnswering(t *testing.T) {
	oldStall, oldDelay := nativeStall, nativeRetryDelay
	nativeStall, nativeRetryDelay = 50*time.Millisecond, time.Millisecond
	t.Cleanup(func() { nativeStall, nativeRetryDelay = oldStall, oldDelay })
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); server.Close() })
	if _, err := fetchNativeChecksum(context.Background(), io.Discard, server.URL+"/linux-amd64-codex.sha256"); !errors.Is(err, errNativeStall) {
		t.Fatal(err)
	}
}
