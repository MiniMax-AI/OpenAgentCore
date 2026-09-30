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

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestNativeToolSearchPublicExecution(t *testing.T) {
	python, binary, root, optionsFile := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON"), os.Getenv("OAC_TEST_NATIVE_DAEMON_BIN"), os.Getenv("OAC_TEST_NATIVE_PROOF_DIR"), os.Getenv("OAC_TEST_TOOL_SEARCH_REAL_OPTIONS")
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
	h := newDispatchHarness(t)
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) { return options, nil }
	home, err := os.MkdirTemp(root, "tool-search-public-")
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
	handler, err := api.NewHandler(h.s, auth, "claude_sdk", api.WithExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	stop := startNativeEngineDaemon(t, h, home, binary, "claude_sdk")
	defer func() { stop() }()
	evidence := filepath.Join(home, "public.json")
	run := func(stage string) {
		cmd := exec.CommandContext(ctx, python, "../../tests/official_tool_search.py", server.URL, token, foreign, model, stage, evidence)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tool search %s: %v %s; evidence %s", stage, err, output, home)
		}
	}
	run("initial")
	var proof struct {
		Session string `json:"session"`
		Turn    string `json:"turn"`
		Call    string `json:"call"`
	}
	raw, err = os.ReadFile(evidence)
	if err != nil || json.Unmarshal(raw, &proof) != nil {
		t.Fatal("invalid evidence", err)
	}
	call, err := h.s.GetFunctionCall(ctx, h.tenant, proof.Session, proof.Turn, proof.Call)
	if err != nil || !call.Applied {
		t.Fatal("function application receipt missing", err)
	}
	turn, err := h.s.GetTurn(ctx, h.tenant, proof.Session, proof.Turn)
	if err != nil {
		t.Fatal(err)
	}
	var outcome execution.Result
	if json.Unmarshal(turn.Outcome, &outcome) != nil || outcome.Done.Usage.Raw["claude_sdk_result"] == nil || outcome.AppliedThrough < 1 {
		t.Fatal("native usage or input receipt missing")
	}
	before, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || before.NativeSessionID == "" {
		t.Fatal("native binding missing", err)
	}
	stop()
	stop = startNativeEngineDaemon(t, h, home, binary, "claude_sdk")
	run("resume")
	after, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || before.NativeSessionID != after.NativeSessionID {
		t.Fatal("native history changed", err)
	}
	t.Logf("Real tool search SDK/raw HTTP, function receipt, cold daemon recovery and isolation passed: %s", home)
}
