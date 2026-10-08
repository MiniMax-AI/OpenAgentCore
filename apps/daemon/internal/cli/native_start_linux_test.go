//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/google/uuid"
)

// The launcher hands oac-sandbox-io a private bootstrap file for the enrolled
// resource and, after the process exits, enrolls again and starts it with the
// new generation.
func TestNativeStartRestartsSandboxIOAfterEnrolling(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	tenant, resource := uuid.NewString(), uuid.NewString()
	var enrolls atomic.Uint64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if r.URL.Path != "/api/v1/agent-daemon/enroll" || r.Header.Get("Authorization") != "Bearer private-test-credential" || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"link_url": "wss://core.example/api/v1/sandbox-link", "resource": map[string]any{
			"tenant_id": tenant, "environment_id": body["environment_id"], "kind": "enrollment", "id": resource, "generation": enrolls.Add(1)}})
	}))
	defer server.Close()
	for i := range args {
		if args[i] == "--remote" {
			args[i+1] = "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/agent-daemon/ws"
		}
	}
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	var config nativeInstallation
	if err := readNativeJSON(filepath.Join(root, "daemon", "installation.json"), &config); err != nil {
		t.Fatal(err)
	}
	// The fake service records its bootstrap file and mode, then exits.
	runs := filepath.Join(t.TempDir(), "runs")
	script := "#!/bin/sh\n[ \"$1\" = --bootstrap-file ] || exit 9\necho \"$(stat -c %a \"$2\") $(cat \"$2\")\" >> '" + runs + "'\nexit 3\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "oac-sandbox-io"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	previous := sandboxRestartDelay
	sandboxRestartDelay = [2]time.Duration{time.Millisecond, 2 * time.Millisecond}
	defer func() { sandboxRestartDelay = previous }()
	output := new(lockedBuffer)
	rc.stdout, rc.stderr = output, output

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runSandboxLauncher(ctx, rc, false, root, config) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if raw, _ := os.ReadFile(runs); strings.Count(string(raw), "\n") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("oac-sandbox-io was not restarted: %s", output.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(runs)
	for i, line := range strings.Split(string(raw), "\n")[:2] {
		mode, document, _ := strings.Cut(line, " ")
		in, err := sandboxbootstrap.Decode([]byte(document))
		if err != nil || mode != "600" {
			t.Fatalf("run %d: mode %s: %v", i, mode, err)
		}
		want := sandboxbootstrap.Resource{TenantID: tenant, EnvironmentID: config.Environment, Kind: "enrollment", ID: resource, Generation: uint64(i + 1)}
		if in.LinkURL != "wss://core.example/api/v1/sandbox-link" || in.Credential != "private-test-credential" || in.Resource != want {
			t.Fatalf("run %d: unexpected bootstrap input %+v", i, in.Resource)
		}
	}
	if strings.Contains(output.String(), "private-test-credential") {
		t.Fatal("credential leaked")
	}
}
