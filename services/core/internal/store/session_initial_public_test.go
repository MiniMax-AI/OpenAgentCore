package store_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestInitialSessionInputOfficialClient(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, _ := store.NewTestStore(t)
	token, foreign := uuid.NewString(), uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: uuid.NewString()}, {OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise real worker admission with dispatch paused for deterministic reads.
	worker, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopped, cancel := context.WithCancel(context.Background())
		cancel()
		if err := worker.Run(stopped); err != context.Canceled {
			t.Error(err)
		}
	})
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	unsupported, err := api.NewHandler(s, auth, "fake_alpha", api.WithExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	other := httptest.NewServer(unsupported)
	defer other.Close()
	command := exec.CommandContext(t.Context(), python, "../../tests/official_session_initial_input.py", server.URL, token, foreign, other.URL)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("official initial input: %v %s", err, output)
	}
}
