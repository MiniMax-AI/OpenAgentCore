package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestEnvironmentConnectionURL(t *testing.T) {
	for _, valid := range []string{"wss://runtime.example/api/v1/agent-daemon/ws", "ws://127.0.0.1:123/api/v1/agent-daemon/ws", "ws://[::1]:123/api/v1/agent-daemon/ws", "ws://runtime.example/api/v1/agent-daemon/ws"} {
		base, err := environmentBase(valid)
		if err != nil || !strings.HasSuffix(base, "/api/v1") {
			t.Fatalf("valid URL rejected: %v", err)
		}
	}
	for _, invalid := range []string{"https://runtime.example/api/v1/agent-daemon/ws", "wss://secret@runtime.example/api/v1/agent-daemon/ws", "wss://runtime.example/api/v1/agent-daemon/ws?secret=value", "wss://runtime.example/api/v1/agent-daemon/ws#fragment", "wss://runtime.example/api/v1/agent-daemon/ws/", "wss://runtime.example/api%2fv1/agent-daemon/ws"} {
		if _, err := environmentBase(invalid); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid URL accepted or disclosed")
		}
	}
}

func TestEnvironmentTransportRejectsRedirectAndUntrustedBodies(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	for _, status := range []int{301, 302, 307, 308, 401, 409, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private-response-canary"))
			}))
			defer server.Close()
			_, err := requestEnrollment(context.Background(), environmentClient(), server.URL, uuid.NewString(), "private-canary")
			if err == nil || strings.Contains(err.Error(), "canary") {
				t.Fatal("enrollment error exposed body or accepted failure")
			}
		})
	}
	if leaked {
		t.Fatal("credential redirected")
	}
}

func TestExecutorCredentialFile(t *testing.T) {
	environment := uuid.NewString()
	path := filepath.Join(t.TempDir(), "key.json")
	write := func(token string) {
		raw, _ := json.Marshal(map[string]string{"key_id": uuid.NewString(), "executor_token": token, "environment_id": environment})
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("first-canary")
	if _, token, err := executorCredential(path, environment); err != nil || token != "first-canary" {
		t.Fatal("valid key rejected")
	}
	write("rotated-canary")
	if _, token, err := executorCredential(path, environment); err != nil || token != "rotated-canary" {
		t.Fatal("rotation not read")
	}
	if _, _, err := executorCredential(path, uuid.NewString()); err == nil {
		t.Fatal("foreign restriction accepted")
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executorCredential(link, environment); err != nil {
		t.Fatal("operator symlink rejected", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executorCredential(path, environment); err != nil {
		t.Fatal("operator permissions rejected", err)
	}
}
