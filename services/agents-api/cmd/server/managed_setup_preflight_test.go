package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestE2BRejectedSpecificationHasSafeActionableDiagnostic(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "provider")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"Version\":1,\"ErrorCode\":\"invalid\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTS_API_E2B_PROVIDER_BIN", helper)
	t.Setenv("AGENTS_API_E2B_STATE_DIR", state)
	id := uuid.NewString()
	s := &managedSetup{installationID: id}
	_, err := s.prepare(t.Context(), store.SandboxSetup{InstallationID: id, Provider: "e2b", Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 3, MemoryMiB: 3072}}, E2B: &store.SandboxE2BConfiguration{APIKey: "synthetic-private-key", Template: "runtime:" + uuid.NewString()}})
	var invalid *store.SandboxConfigurationError
	if !errors.As(err, &invalid) || !strings.Contains(invalid.Message, "CPU and memory") || strings.Contains(invalid.Message, "synthetic-private-key") || s.selected.Load() != nil {
		t.Fatal("rejected candidate lost its safe diagnostic or was published", err)
	}
}
