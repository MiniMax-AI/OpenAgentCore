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

// Run once per engine with a real provider and a daemon containing that adapter.
// Native tool inventory qualification is separate from these public API checks.
func TestNativeToolPolicyPublicExecution(t *testing.T) {
	python, binary, root, optionsFile := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON"), os.Getenv("PARSAR_NATIVE_DAEMON_BIN"), os.Getenv("PARSAR_NATIVE_PROOF_DIR"), os.Getenv("PARSAR_TOOL_POLICY_REAL_OPTIONS")
	if python == "" || binary == "" || root == "" || optionsFile == "" {
		t.Skip("native daemon, pinned SDK, private real-model options and evidence directory required")
	}
	kind := os.Getenv("PARSAR_TOOL_POLICY_ENGINE")
	if kind != "codex" && kind != "claude_sdk" && kind != "mcode" {
		t.Fatal("tool policy acceptance requires codex, claude_sdk or mcode")
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
	home, err := os.MkdirTemp(root, "tool-policy-"+kind+"-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
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
	token, foreign, foreignTenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test", ProjectID: h.tenant, SubjectKind: "service_account", SubjectID: "owner", TokenSHA256: device.HashCredential(token), TenantID: h.tenant},
		{OrganizationID: "test", ProjectID: foreignTenant, SubjectKind: "service_account", SubjectID: "other", TokenSHA256: device.HashCredential(foreign), TenantID: foreignTenant},
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
		cmd := exec.CommandContext(ctx, python, "../../tests/official_tool_policy.py", server.URL, token, foreign, model, stage, evidence)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tool policy %s: %v %s; evidence %s", stage, err, output, home)
		}
	}
	run("initial")
	var proof struct {
		Sessions []struct {
			ID        string `json:"id"`
			FirstTurn string `json:"first_turn"`
		} `json:"sessions"`
		Omitted []string `json:"omitted"`
	}
	raw, err = os.ReadFile(evidence)
	if err != nil || json.Unmarshal(raw, &proof) != nil || len(proof.Sessions) != 4 || len(proof.Omitted) != 2 {
		t.Fatal("invalid public evidence", err)
	}
	page, err := h.s.ListSessions(ctx, h.tenant, "", 100, true, nil)
	if err != nil || len(page.Sessions) != 1+len(proof.Sessions)+len(proof.Omitted) {
		t.Fatal("rejected configuration persisted a Session", err)
	}
	foreignPage, err := h.s.ListSessions(ctx, foreignTenant, "", 100, true, nil)
	if err != nil || len(foreignPage.Sessions) != 0 {
		t.Fatal("foreign Agent reference persisted a Session", err)
	}
	nativeIDs := make(map[string]string, len(proof.Sessions))
	for _, item := range proof.Sessions {
		session, err := h.s.GetSession(ctx, h.tenant, item.ID)
		if err != nil || session.Engine != kind {
			t.Fatal("selected engine was not persisted", err)
		}
		turn, err := h.s.GetTurn(ctx, h.tenant, item.ID, item.FirstTurn)
		if err != nil {
			t.Fatal(err)
		}
		inputs, err := h.s.ListTurnInputs(ctx, h.tenant, item.ID, item.FirstTurn, 0, 100)
		var outcome execution.Result
		if err != nil || len(inputs) != 1 || json.Unmarshal(turn.Outcome, &outcome) != nil || outcome.AppliedThrough != inputs[0].Sequence {
			t.Fatal("native text input receipt missing", err)
		}
		binding, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, item.ID)
		if err != nil || binding.NativeSessionID == "" {
			t.Fatal("native binding missing", err)
		}
		nativeIDs[item.ID] = binding.NativeSessionID
	}
	stop()
	stop = startNativeEngineDaemon(t, h, home, binary, kind)
	run("resume")
	for id, before := range nativeIDs {
		after, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, id)
		if err != nil || after.NativeSessionID != before {
			t.Fatal("cold continuation changed native history", err)
		}
	}
	t.Logf("Real %s disabled tool policy SDK/raw HTTP, native receipts, cold continuation and tenant isolation passed: %s", kind, home)
}
