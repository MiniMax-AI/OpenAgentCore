//go:build linux

package main

import (
	"context"
	"errors"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAllocationLockSurvivesCallerDeadlineUntilExplicitSettlement(t *testing.T) {
	home := t.TempDir()
	q := wire.Request{Config: wire.Config{RuntimeHome: home}, Deadline: time.Now().Add(time.Second)}
	release, e := allocationLock(q)
	if e != nil {
		t.Fatal(e)
	}
	waiting := q
	waiting.Deadline = time.Now().Add(40 * time.Millisecond)
	if next, e := allocationLock(waiting); !errors.Is(e, context.DeadlineExceeded) {
		if next != nil {
			next()
		}
		t.Fatalf("second lifecycle entered: %v", e)
	}
	release()
	q.Deadline = time.Now().Add(time.Second)
	release, e = allocationLock(q)
	if e != nil {
		t.Fatal(e)
	}
	release()
}
func TestLockDirectoryCannotRedirectIntoAnotherHome(t *testing.T) {
	home := t.TempDir()
	foreign := t.TempDir()
	if e := os.Symlink(foreign, filepath.Join(home, "oac-locks")); e != nil {
		t.Fatal(e)
	}
	_, e := allocationLock(wire.Request{Config: wire.Config{RuntimeHome: home}, Deadline: time.Now().Add(time.Second)})
	if e == nil {
		t.Fatal("symlink lock directory accepted")
	}
}
