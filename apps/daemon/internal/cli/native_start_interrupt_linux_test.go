//go:build linux

package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeStartInterruptHelper(t *testing.T) {
	if os.Getenv("OAC_TEST_START_INTERRUPT") != "1" {
		t.Skip("subprocess helper")
	}
	if err := runStart(&runContext{stdout: os.Stdout, stderr: os.Stderr}, nil); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestNativeStartInterruptCancelsEnrollment(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	requested := make(chan struct{})
	release := make(chan struct{})
	// The enrollment never answers before the interrupt.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(requested)
		<-release
	}))
	defer server.Close()
	defer close(release)
	for i := range args {
		if args[i] == "--remote" {
			args[i+1] = "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/agent-daemon/ws"
		}
	}
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativeStartInterruptHelper$")
	cmd.Env = append(os.Environ(), "OAC_TEST_START_INTERRUPT=1")
	output := new(bytes.Buffer)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-requested:
	case <-time.After(10 * time.Second):
		t.Fatal("start did not reach enrollment")
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted startup succeeded")
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("start ignored interrupt while enrollment was pending")
	}
	if strings.Contains(output.String(), "backgrounded") {
		t.Fatal("interrupted startup spawned a daemon")
	}
	if _, err := os.Stat(filepath.Join(root, "daemon", "default", "connect.pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("interrupted startup published a background process")
	}
}
