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

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestNativeFunctionImagePublicExecution(t *testing.T) {
	python, binary, root, optionsFile := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON"), os.Getenv("OAC_TEST_NATIVE_DAEMON_BIN"), os.Getenv("OAC_TEST_NATIVE_PROOF_DIR"), os.Getenv("OAC_TEST_FUNCTION_IMAGE_REAL_OPTIONS")
	if python == "" || binary == "" || root == "" || optionsFile == "" {
		t.Skip("native daemon, fixed SDK, real model options and evidence directory required")
	}
	raw, err := os.ReadFile(optionsFile)
	if err != nil {
		t.Fatal(err)
	}
	var options map[string]any
	if json.Unmarshal(raw, &options) != nil {
		t.Fatal("invalid private options")
	}
	model, _ := options["model"].(string)
	if model == "" {
		t.Fatal("real model required")
	}
	kind := os.Getenv("OAC_TEST_FUNCTION_IMAGE_ENGINE")
	if kind != "codex" && kind != "claude_sdk" {
		t.Fatal("image acceptance requires a specified native engine")
	}
	h := newDispatchHarness(t)
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) { return options, nil }
	home, err := os.MkdirTemp(root, "function-image-public-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
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
		case <-time.After(20 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	token, foreign := uuid.NewString(), uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{
		{OrganizationID: "test", ProjectID: h.tenant, SubjectKind: "service_account", SubjectID: "owner", TokenSHA256: device.HashCredential(token), TenantID: h.tenant},
		{OrganizationID: "test", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "other", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(h.s, auth, kind, api.WithExecution(worker), api.WithExecutionPolicy(h.d.Policy))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	stop := startNativeEngineDaemon(t, h, home, binary, kind)
	defer func() { stop() }()
	evidence := filepath.Join(home, "public.json")
	run := func(stage string) {
		cmd := exec.CommandContext(ctx, python, "../../tests/official_function_images.py", server.URL, token, foreign, model, stage, evidence)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("function images %s: %v %s; evidence %s", stage, err, output, home)
		}
	}
	run("initial")
	var proof struct {
		Session string                        `json:"session"`
		Calls   []struct{ Turn, Call string } `json:"calls"`
	}
	raw, err = os.ReadFile(evidence)
	if err != nil || json.Unmarshal(raw, &proof) != nil || len(proof.Calls) != 4 {
		t.Fatal("invalid evidence", err)
	}
	for _, item := range proof.Calls {
		call, err := h.s.GetFunctionCall(ctx, h.tenant, proof.Session, item.Turn, item.Call)
		if err != nil || !call.Applied {
			t.Fatal("function delivery acknowledgement missing", err)
		}
		inputs, err := h.s.ListTurnInputs(ctx, h.tenant, proof.Session, item.Turn, 0, 100)
		if err != nil || len(inputs) != 2 || inputs[0].Kind != "message" || inputs[1].Kind != "tool_result" {
			t.Fatal("function result admission duplicated or mutated", err)
		}
	}
	before, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || before.NativeSessionID == "" {
		t.Fatal("native binding missing", err)
	}
	stop()
	stop = startNativeEngineDaemon(t, h, home, binary, kind)
	run("resume")
	after, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || before.NativeSessionID != after.NativeSessionID {
		t.Fatal("native history changed", err)
	}
	t.Logf("Real function image SDK/raw HTTP, delivery, cold daemon recovery and isolation passed: %s", home)
}
