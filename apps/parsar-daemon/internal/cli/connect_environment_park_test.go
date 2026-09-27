package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/transport"
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

// A permanent enrollment rejection parks the self-hosted daemon: one message,
// no further requests, and SIGTERM ends it with exit 0, so Docker's
// unless-stopped policy has nothing to restart. Transient failures still exit 1.
func TestEnvironmentRejectionParksUntilTerminated(t *testing.T) {
	if argv := os.Getenv("OAC_TEST_ENVIRONMENT_CONNECT"); argv != "" {
		var args []string
		if json.Unmarshal([]byte(argv), &args) != nil {
			os.Exit(2)
		}
		if err := Execute(args); err != nil {
			fmt.Fprintln(os.Stderr, "oac-daemon:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
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
			home := t.TempDir()
			environment, keyID := uuid.NewString(), uuid.NewString()
			credential := filepath.Join(home, "executor-key.json")
			raw, _ := json.Marshal(map[string]string{"key_id": keyID, "environment_id": environment, "executor_token": "private-canary"})
			if err := os.WriteFile(credential, raw, 0600); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal([]string{"connect", "--remote", "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/agent-daemon/ws",
				"--environment-id", environment, "--credential-file", credential, "--self-hosted-install"})
			cmd := exec.Command(os.Args[0], "-test.run=^TestEnvironmentRejectionParksUntilTerminated$")
			cmd.Env = append(os.Environ(), "OAC_TEST_ENVIRONMENT_CONNECT="+string(args), "OAC_RUNTIME_HOME="+home)
			var stderr lockedBuffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			if tc.message == "" {
				var exit *exec.ExitError
				select {
				case err := <-exited:
					if !errors.As(err, &exit) || exit.ExitCode() != 1 || strings.Contains(stderr.String(), "will not retry") {
						t.Fatalf("transient rejection did not exit 1: %v %s", err, stderr.String())
					}
				case <-time.After(10 * time.Second):
					_ = cmd.Process.Kill()
					t.Fatal("transient rejection parked")
				}
				return
			}
			for deadline := time.Now().Add(10 * time.Second); !strings.Contains(stderr.String(), "will not retry"); {
				select {
				case err := <-exited:
					t.Fatalf("exited instead of parking: %v %s", err, stderr.String())
				case <-time.After(20 * time.Millisecond):
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatal("no park message")
				}
			}
			time.Sleep(time.Second)
			if requests.Load() != 1 {
				t.Fatalf("parked daemon made %d requests", requests.Load())
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-exited:
				if err != nil {
					t.Fatalf("SIGTERM exit: %v", err)
				}
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatal("parked daemon ignored SIGTERM")
			}
			output := stderr.String()
			if strings.Count(output, "will not retry") != 1 || !strings.Contains(output, tc.message) || !strings.Contains(output, keyID) ||
				!strings.Contains(output, environment) || strings.Contains(output, "private-canary") {
				t.Fatalf("park message = %s", output)
			}
		})
	}
}

func TestEnvironmentRejectionClassifiesConnectionErrors(t *testing.T) {
	for _, tc := range []struct {
		err        error
		selfHosted bool
		want       string
	}{
		{fmt.Errorf("connect: runtime deleted: %w", transport.ErrPermanent), true, "rerun the self-hosted install command"},
		{fmt.Errorf("connect: runtime deleted: %w", transport.ErrPermanent), false, "install it for this Runtime and restart it"},
		{fmt.Errorf("connect: permanent error: %w: %w", transport.ErrPermanent, transport.ErrIncompatibleVersion), true, "daemon version"},
		{errors.New("connect: bootstrap: Environment bootstrap failed"), true, ""},
		{nil, true, ""},
	} {
		got := environmentRejection(tc.err, uuid.NewString(), uuid.NewString(), tc.selfHosted)
		if tc.want == "" && got != "" || !strings.Contains(got, tc.want) || !tc.selfHosted && strings.Contains(got, "container") {
			t.Errorf("environmentRejection(%v, %v) = %q", tc.err, tc.selfHosted, got)
		}
	}
}
