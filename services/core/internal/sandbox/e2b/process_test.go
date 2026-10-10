package e2b

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestProcessWireRejectsUnknownAndTrailingOutput(t *testing.T) {
	for _, output := range []string{`{"Version":1,"Secret":"private"}`, `{"Version":1} {"Version":1}`} {
		root := t.TempDir()
		binary := filepath.Join(root, "helper")
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' '"+output+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := (&ProcessCaller{}).Call(bounded(t), Request{Version: ProtocolVersion, Operation: "list_templates", Config: Config{Binary: binary}, Deadline: time.Now().Add(time.Minute)}); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid helper response accepted or leaked", err)
		}
	}
}
func TestTimeoutDoesNotTerminateOwnedHelper(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "helper")
	marker := filepath.Join(root, "settled")
	// The private test path contains no shell syntax; the real adapter never puts
	// credentials or request contents in argv or inherited environment.
	script := "#!/bin/sh\nsleep 0.15\ntouch '" + marker + "'\nprintf '%s' '{\"Version\":" + strconv.Itoa(ProtocolVersion) + "}'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := (&ProcessCaller{}).Call(ctx, Request{Version: ProtocolVersion, Operation: "list_templates", Config: Config{Binary: binary}, Deadline: time.Now().Add(time.Minute)}); err == nil {
		t.Fatal("timeout not reported")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("helper was killed before it could settle")
}
func TestEnvironmentDropsProviderSelectorsAndCredentials(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "helper")
	script := "#!/bin/sh\nif test -n \"${E2B_API_KEY:-}${E2B_API_URL:-}${PRIVATE_MODEL_KEY:-}\"; then exit 1; fi\nprintf '%s' '{\"Version\":" + strconv.Itoa(ProtocolVersion) + "}'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("E2B_API_KEY", "secret")
	t.Setenv("E2B_API_URL", "https://wrong.example")
	t.Setenv("PRIVATE_MODEL_KEY", "secret")
	if _, err := (&ProcessCaller{}).Call(bounded(t), Request{Version: ProtocolVersion, Operation: "list_templates", Config: Config{Binary: binary}, Deadline: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialFenceWaitsForActualHelperExitAfterCancellation(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "helper")
	marker := filepath.Join(root, "started")
	finish := filepath.Join(root, "finish")
	script := "#!/bin/sh\ntouch '" + marker + "'\nwhile ! test -f '" + finish + "'; do sleep 0.01; done\nprintf '%s' '{\"Version\":" + strconv.Itoa(ProtocolVersion) + "}'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	fence := &sandbox.CallFence{}
	ctx, cancel := context.WithCancel(t.Context())
	entered, err := fence.Enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := (&ProcessCaller{Fence: fence}).Call(ctx, Request{Version: ProtocolVersion, Operation: "list_templates", Config: Config{Binary: binary}, Deadline: time.Now().Add(time.Minute)})
		entered()
		done <- err
	}()
	t.Cleanup(func() { os.WriteFile(finish, nil, 0600); cancel() })
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("caller cancellation hidden")
	}
	blocked, stop := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer stop()
	if unlock, err := fence.Fence(blocked); err == nil {
		unlock()
		t.Fatal("credential fence forgot a live helper")
	}
	// A failed exclusive waiter must return its permits immediately.
	if release, err := fence.Enter(t.Context()); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	if err := os.WriteFile(finish, nil, 0600); err != nil {
		t.Fatal(err)
	}
	final, end := context.WithTimeout(t.Context(), 3*time.Second)
	defer end()
	release, err := fence.Fence(final)
	if err != nil {
		t.Fatal("late helper exit did not release fence", err)
	}
	release()
	if release, err := fence.Enter(final); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}
