package e2b

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessWireRejectsUnknownAndTrailingOutput(t *testing.T) {
	for _, output := range []string{`{"Version":1,"Secret":"private"}`, `{"Version":1} {"Version":1}`} {
		root := t.TempDir()
		binary := filepath.Join(root, "helper")
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' '"+output+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := (&ProcessCaller{}).Call(bounded(t), Request{Config: Config{Binary: binary}}); err == nil || strings.Contains(err.Error(), "private") {
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
	script := "#!/bin/sh\nsleep 0.15\ntouch '" + marker + "'\nprintf '%s' '{\"Version\":1}'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := (&ProcessCaller{}).Call(ctx, Request{Config: Config{Binary: binary}}); err == nil {
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
	script := "#!/bin/sh\nif test -n \"${E2B_API_KEY:-}${E2B_API_URL:-}${PRIVATE_MODEL_KEY:-}\"; then exit 1; fi\nprintf '%s' '{\"Version\":1}'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("E2B_API_KEY", "secret")
	t.Setenv("E2B_API_URL", "https://wrong.example")
	t.Setenv("PRIVATE_MODEL_KEY", "secret")
	if _, err := (&ProcessCaller{}).Call(bounded(t), Request{Config: Config{Binary: binary}}); err != nil {
		t.Fatal(err)
	}
}
