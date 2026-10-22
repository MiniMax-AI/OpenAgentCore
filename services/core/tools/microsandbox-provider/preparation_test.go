//go:build linux

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

func TestExpiredPreparationIsNotRestored(t *testing.T) {
	b := backend{q: wire.Request{Config: wire.Config{RuntimeHome: t.TempDir(), Image: "image@sha256:" + strings.Repeat("a", 64)}, Key: strings.Repeat("b", 64), Deadline: time.Now().Add(time.Minute)}}
	if err := privateDirectory(b.preparationStore()); err != nil {
		t.Fatal(err)
	}
	entry := b.preparationEntry()
	if err := os.WriteFile(entry, []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if !validPreparation(entry, time.Now()) {
		t.Fatal("fresh entry rejected")
	}
	old := time.Now().Add(-preparationTTL - time.Minute)
	if err := os.Chtimes(entry, old, old); err != nil {
		t.Fatal(err)
	}
	// An expired entry returns before the helper touches any compute.
	if restored, err := b.restorePreparation(context.Background()); restored || err != nil {
		t.Fatal(restored, err)
	}
}
