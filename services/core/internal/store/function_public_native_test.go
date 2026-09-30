package store_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func TestNativePublicFunctionExecution(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	h, ctx, home := nativeDispatchHarness(t)
	model, output, requests := nativeFunctionModel(t, home)
	defer model.Close()
	serverURL, token := nativePublicFunctionServer(t, h, ctx, nativeModelProvider(model))
	outputPath, proofPath := filepath.Join(home, "function-output.json"), filepath.Join(home, "public-functions.json")
	raw, _ := json.Marshal(output)
	if err := os.WriteFile(outputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, python, "../../tests/official_functions.py", serverURL, token, outputPath, proofPath)
	if log, err := command.CombinedOutput(); err != nil {
		t.Fatalf("official native functions: %v %s", err, log)
	}
	var proof struct {
		Session string   `json:"session"`
		Turns   []string `json:"turns"`
		Calls   []string `json:"calls"`
	}
	raw, err := os.ReadFile(proofPath)
	if err != nil || json.Unmarshal(raw, &proof) != nil || len(proof.Turns) != 3 || len(proof.Calls) != 3 {
		t.Fatal(proof, err)
	}
	for i, callID := range proof.Calls {
		call, err := h.s.GetFunctionCall(ctx, h.tenant, proof.Session, proof.Turns[i], callID)
		if err != nil || call.Applied != (i < 2) {
			t.Fatal(call, err)
		}
	}
	bound, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || bound.NativeSessionID == "" || bound.Device.ID != h.device.ID {
		t.Fatal(bound, err)
	}
	if requests.Load() != 5 {
		t.Fatal("unexpected replay or missing native continuation", requests.Load())
	}
	t.Logf("Official SDK configured functions, native text/image/error results, application receipts, next Turn and cancellation passed; evidence %s", home)
}

func nativePublicFunctionServer(t *testing.T, h *dispatchHarness, ctx context.Context, provider *v1.ModelProviderInput) (string, string) {
	t.Helper()
	worker := startWorker(t, ctx, h.db, h.d)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("worker did not stop")
		}
	})
	token := uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: h.tenant}})
	handler, err := publicHandler(t, h.s, h.db, auth, "codex", workerExecution(worker), nativeDeploymentDefaults("gpt-5.5", provider))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL, token
}
