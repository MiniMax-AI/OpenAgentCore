//go:build linux

package cli

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestResumeControlRejectsStaleIdentityAndDiscardsPreParkSignals(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "control.json")
	t.Setenv(suspendControlEnv, path)
	t.Setenv("PARSAR_RUNTIME_ENVIRONMENT_ID", "env")
	control, err := newSuspendControl()
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	control.signal <- syscall.SIGUSR1
	if err := control.Arm(proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "current"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if err := control.Wait(ctx); err == nil {
		t.Fatal("stale signal woke future suspension")
	}
	cancel()
	if err := runResume(nil, []string{"--control-file", path, "--environment-id", "env", "--suspend-id", "stale"}); err == nil {
		t.Fatal("stale suspension accepted")
	}
	saved := control.identity.StartTime
	control.identity.StartTime = "wrong"
	if err := control.save(); err != nil {
		t.Fatal(err)
	}
	if err := runResume(nil, []string{"--control-file", path, "--environment-id", "env", "--suspend-id", "current"}); err == nil {
		t.Fatal("reused PID accepted")
	}
	control.identity.StartTime = saved
	if err := control.save(); err != nil {
		t.Fatal(err)
	}
	if err := runResume(nil, []string{"--control-file", path, "--environment-id", "env", "--suspend-id", "current"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := control.Wait(ctx); err != nil {
		t.Fatal("validated pidfd signal did not wake owner", err)
	}
	if err := control.Disarm(); err != nil {
		t.Fatal(err)
	}
	if err := runResume(nil, []string{"--control-file", path, "--environment-id", "env", "--suspend-id", "current"}); err == nil {
		t.Fatal("completed suspension accepted another wake")
	}
}
