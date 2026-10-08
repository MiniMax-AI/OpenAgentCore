//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A permanent enrollment rejection parks the launcher: one message, no further
// requests, and its signal ends it without an error, so a restart policy has
// nothing to restart. Transient failures still fail.
func TestEnvironmentRejectionParksUntilTerminated(t *testing.T) {
	for _, tc := range []struct {
		status  int
		message string
	}{
		{http.StatusUnauthorized, "was rejected by Core"},
		{http.StatusConflict, "is bound to a different executor credential"},
		{http.StatusServiceUnavailable, ""},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			environment, keyID := uuid.NewString(), uuid.NewString()
			credential := filepath.Join(t.TempDir(), "executor-key.json")
			raw, _ := json.Marshal(map[string]string{"key_id": keyID, "environment_id": environment, "executor_token": "private-canary"})
			if err := os.WriteFile(credential, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			config := nativeInstallation{Remote: "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/agent-daemon/ws", Environment: environment, Credential: credential}
			output := new(lockedBuffer)
			rc := &runContext{stdout: output, stderr: output}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			exited := make(chan error, 1)
			go func() { exited <- runSandboxLauncher(ctx, rc, false, t.TempDir(), config) }()
			if tc.message == "" {
				select {
				case err := <-exited:
					if err == nil || strings.Contains(output.String(), "will not retry") {
						t.Fatalf("transient rejection = %v %s", err, output.String())
					}
				case <-time.After(10 * time.Second):
					t.Fatal("transient rejection parked")
				}
				return
			}
			for deadline := time.Now().Add(10 * time.Second); !strings.Contains(output.String(), "will not retry"); {
				select {
				case err := <-exited:
					t.Fatalf("exited instead of parking: %v %s", err, output.String())
				case <-time.After(20 * time.Millisecond):
				}
				if time.Now().After(deadline) {
					t.Fatal("no park message")
				}
			}
			time.Sleep(time.Second)
			if requests.Load() != 1 {
				t.Fatalf("parked launcher made %d requests", requests.Load())
			}
			cancel()
			select {
			case err := <-exited:
				if err != nil {
					t.Fatalf("parked launcher ended with %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("parked launcher ignored its signal")
			}
			got := output.String()
			if strings.Count(got, "will not retry") != 1 || !strings.Contains(got, tc.message) || !strings.Contains(got, keyID) ||
				!strings.Contains(got, environment) || strings.Contains(got, "private-canary") {
				t.Fatalf("park message = %s", got)
			}
		})
	}
}
