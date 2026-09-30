package store_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func verifyNativePublicExecution(t *testing.T, h *dispatchHarness, parent context.Context, evidence string, provider *v1.ModelProviderInput) {
	t.Helper()
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Log("Public SDK proof requires OAC_TEST_OFFICIAL_SDK_PYTHON")
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	token, foreign := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: h.tenant}, {OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()}})
	handler, err := publicHandler(t, h.s, auth, "codex", workerExecution(worker), nativeDeploymentDefaults(h.s, "gpt-5.5", provider))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	command := exec.CommandContext(ctx, python, "../../tests/official_execution.py", server.URL, token, foreign, filepath.Join(evidence, "public-execution.json"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("official SDK native execution: %v\n%s", err, output)
	}
	t.Logf("Official SDK public execution, retry identity, isolation, result recovery and native cancellation passed: %s", evidence)
}
