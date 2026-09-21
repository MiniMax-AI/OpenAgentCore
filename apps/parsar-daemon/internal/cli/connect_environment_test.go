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

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/auth"
	"github.com/google/uuid"
)

func TestEnvironmentConnectionURL(t *testing.T) {
	for _, valid := range []string{"wss://runtime.example/api/v1/agent-daemon/ws", "ws://127.0.0.1:123/api/v1/agent-daemon/ws", "ws://[::1]:123/api/v1/agent-daemon/ws"} {
		base, err := environmentBase(valid)
		if err != nil || !strings.HasSuffix(base, "/api/v1") {
			t.Fatalf("valid URL rejected: %v", err)
		}
	}
	for _, invalid := range []string{"ws://runtime.example/api/v1/agent-daemon/ws", "https://runtime.example/api/v1/agent-daemon/ws", "wss://secret@runtime.example/api/v1/agent-daemon/ws", "wss://runtime.example/api/v1/agent-daemon/ws?secret=value", "wss://runtime.example/api/v1/agent-daemon/ws#fragment", "wss://runtime.example/api/v1/agent-daemon/ws/", "wss://runtime.example/api%2fv1/agent-daemon/ws"} {
		if _, err := environmentBase(invalid); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid URL accepted or disclosed")
		}
	}
}

func TestEnvironmentEnrollmentAndBootstrap(t *testing.T) {
	environment := uuid.NewString()
	want := environmentEnrollment{uuid.NewString(), uuid.NewString(), environment, "/workspace"}
	var remote string
	var enrolls, bootstraps int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-canary" {
			t.Error("wrong authorization")
		}
		if r.Method != http.MethodPost {
			t.Error("wrong method")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/api/v1/agent-daemon/enroll":
			enrolls++
			if len(body) != 1 || body["environment_id"] != environment {
				t.Error("wrong enrollment body")
			}
			_ = json.NewEncoder(w).Encode(want)
		case "/api/v1/agent-daemon/bootstrap":
			bootstraps++
			if len(body) != 1 || body["device_id"] != want.DeviceID {
				t.Error("wrong bootstrap body")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"device_id": want.DeviceID, "ws_url": remote, "heartbeat_seconds": 15})
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	remote = "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/agent-daemon/ws"
	base, _ := environmentBase(remote)
	got, err := enrollEnvironment(context.Background(), environmentClient(), base, environment, "private-canary")
	if err != nil || got != want {
		t.Fatalf("enrollment: %v", err)
	}
	boot, err := environmentBootstrap(context.Background(), auth.Profile{ServerURL: base, RuntimeID: want.DeviceID, RunnerCredential: "private-canary"}, remote)
	if err != nil || boot.WSURL != remote || enrolls != 1 || bootstraps != 1 {
		t.Fatalf("bootstrap: %v", err)
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
			_, err := enrollEnvironment(context.Background(), environmentClient(), server.URL, uuid.NewString(), "private-canary")
			if err == nil || strings.Contains(err.Error(), "canary") {
				t.Fatal("enrollment error exposed body or accepted failure")
			}
			_, err = environmentBootstrap(context.Background(), auth.Profile{ServerURL: server.URL, RuntimeID: uuid.NewString(), RunnerCredential: "private-canary"}, "unused")
			if err == nil || strings.Contains(err.Error(), "canary") {
				t.Fatal("bootstrap error exposed body or accepted failure")
			}
		})
	}
	if leaked {
		t.Fatal("credential redirected")
	}
}

func TestEnvironmentBootstrapCannotChangeConnection(t *testing.T) {
	device := uuid.NewString()
	for _, response := range []map[string]string{{"device_id": uuid.NewString(), "ws_url": "wss://core/api/v1/agent-daemon/ws"}, {"device_id": device, "ws_url": "wss://other/api/v1/agent-daemon/ws"}, {"device_id": device}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(response) }))
		_, err := environmentBootstrap(context.Background(), auth.Profile{ServerURL: server.URL, RuntimeID: device, RunnerCredential: "canary"}, "wss://core/api/v1/agent-daemon/ws")
		server.Close()
		if err == nil {
			t.Fatal("changed connection accepted")
		}
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
	if token, err := executorCredential(path, environment); err != nil || token != "first-canary" {
		t.Fatal("valid key rejected")
	}
	write("rotated-canary")
	if token, err := executorCredential(path, environment); err != nil || token != "rotated-canary" {
		t.Fatal("rotation not read")
	}
	if _, err := executorCredential(path, uuid.NewString()); err == nil {
		t.Fatal("foreign restriction accepted")
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := executorCredential(link, environment); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := executorCredential(path, environment); err == nil {
		t.Fatal("public credential accepted")
	}
}

func TestEnvironmentBindingPreservesIdentityAndHistory(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	want := environmentBinding{"wss://core/api/v1/agent-daemon/ws", environmentEnrollment{uuid.NewString(), uuid.NewString(), uuid.NewString(), "/workspace"}, "/environment/workspace"}
	if err := saveEnvironmentBinding(root, want); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(root, "parsar-daemon", "agent-sessions", "retained")
	if err := os.MkdirAll(filepath.Dir(history), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history, []byte("unchanged-history"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveEnvironmentBinding(root, want); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*environmentBinding){func(b *environmentBinding) { b.Enrollment.SessionID = uuid.NewString() }, func(b *environmentBinding) { b.Enrollment.EnvironmentID = uuid.NewString() }, func(b *environmentBinding) { b.Enrollment.DeviceID = uuid.NewString() }, func(b *environmentBinding) { b.RemoteURL = "wss://other/api/v1/agent-daemon/ws" }} {
		changed := want
		mutate(&changed)
		if err := saveEnvironmentBinding(root, changed); err == nil {
			t.Fatal("identity overwritten")
		}
	}
	if raw, _ := os.ReadFile(history); string(raw) != "unchanged-history" {
		t.Fatal("history changed")
	}
	if err := os.Remove(filepath.Join(root, "parsar-daemon", "environment.json")); err != nil {
		t.Fatal(err)
	}
	if err := saveEnvironmentBinding(root, want); err == nil {
		t.Fatal("unlabelled history adopted")
	}
}

func TestEnvironmentBindingAllowsPackagedFilesAndRejectsUnsafeReceipt(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	want := environmentBinding{"wss://core/api/v1/agent-daemon/ws", environmentEnrollment{uuid.NewString(), uuid.NewString(), uuid.NewString(), "/workspace"}, "/environment/workspace"}
	if err := os.WriteFile(filepath.Join(root, "installed-bundle"), []byte("bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveEnvironmentBinding(root, want); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "parsar-daemon", "environment.json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := saveEnvironmentBinding(root, want); err == nil {
		t.Fatal("public receipt accepted")
	}
}

func TestEnvironmentTargetCheckedBeforeCredentialTransmission(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARSAR_HOME", root)
	want := environmentBinding{"wss://core/api/v1/agent-daemon/ws", environmentEnrollment{uuid.NewString(), uuid.NewString(), uuid.NewString(), "/workspace"}, "/environment/workspace"}
	if err := saveEnvironmentBinding(root, want); err != nil {
		t.Fatal(err)
	}
	if err := checkEnvironmentTarget(want.RemoteURL, want.Enrollment.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if err := checkEnvironmentTarget("wss://other/api/v1/agent-daemon/ws", want.Enrollment.EnvironmentID); err == nil {
		t.Fatal("new credential recipient accepted")
	}
	if err := checkEnvironmentTarget(want.RemoteURL, uuid.NewString()); err == nil {
		t.Fatal("new Environment accepted")
	}
}

func TestEnvironmentEnrollmentRejectsWrongIdentityAndWorkspace(t *testing.T) {
	environment := uuid.NewString()
	good := environmentEnrollment{uuid.NewString(), uuid.NewString(), environment, "/workspace"}
	for _, mutate := range []func(*environmentEnrollment){func(b *environmentEnrollment) { b.EnvironmentID = uuid.NewString() }, func(b *environmentEnrollment) { b.DeviceID = "" }, func(b *environmentEnrollment) { b.SessionID = "invalid" }, func(b *environmentEnrollment) { b.WorkspaceDirectory = "/different" }} {
		bad := good
		mutate(&bad)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(bad) }))
		_, err := enrollEnvironment(context.Background(), environmentClient(), server.URL, environment, "secret")
		server.Close()
		if err == nil {
			t.Fatal("invalid binding accepted")
		}
	}
}

func TestEnvironmentConnectRejectsPairing(t *testing.T) {
	t.Setenv(connectInlineURLEnv, "")
	t.Setenv(connectInlineTokenEnv, "")
	t.Setenv(connectInlineDeviceNameEnv, "")
	rc := &runContext{stdout: &strings.Builder{}, stderr: &strings.Builder{}}
	err := runConnect(rc, []string{"--remote", "wss://core/api/v1/agent-daemon/ws", "--environment-id", uuid.NewString(), "--credential-file", "/unused", "--token", "private-pairing-canary"})
	if err == nil || strings.Contains(err.Error(), "private-pairing-canary") {
		t.Fatal("pairing accepted or leaked")
	}
}
