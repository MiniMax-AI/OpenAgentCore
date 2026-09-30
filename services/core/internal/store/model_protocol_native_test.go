package store_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

// TestNativeModelProtocolPublicExecution is opt-in and never fabricates a model
// response. Run separately for each private engine/protocol configuration.
func TestNativeModelProtocolPublicExecution(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	binary := os.Getenv("OAC_TEST_NATIVE_DAEMON_BIN")
	root := os.Getenv("OAC_TEST_NATIVE_PROOF_DIR")
	optionsFile := os.Getenv("OAC_TEST_MODEL_PROTOCOL_OPTIONS")
	if python == "" || binary == "" || root == "" || optionsFile == "" {
		t.Skip("fixed SDK, native daemon, private model protocol options and proof directory required")
	}
	raw, err := os.ReadFile(optionsFile)
	if err != nil {
		t.Fatal("cannot read private model protocol options")
	}
	var options struct {
		Engine        string                `json:"engine"`
		Model         string                `json:"model"`
		Provider      v1.ModelProviderInput `json:"model_provider"`
		HarnessConfig json.RawMessage       `json:"harness_config"`
	}
	if json.Unmarshal(raw, &options) != nil || options.Model == "" || options.Provider.BaseURL == "" || options.Provider.APIKey == "" {
		t.Fatal("invalid private model protocol options")
	}
	switch options.Engine {
	case "codex", "claude_sdk", "mcode":
	default:
		t.Fatal("unsupported native test engine")
	}
	switch options.Provider.Protocol {
	case "anthropic", "responses", "chat_completions":
	default:
		t.Fatal("unsupported native test protocol")
	}
	optionsFile, err = filepath.Abs(optionsFile)
	if err != nil {
		t.Fatal("cannot resolve private model protocol options")
	}
	h := newDispatchHarness(t)
	// Deliberately leave Dispatcher.Options unset. Only the public SDK Session
	// creation freezes the deployment default used by the environment:none profile.
	if h.d.Options != nil {
		t.Fatal("fixture must not override public model provider admission")
	}
	home, err := os.MkdirTemp(root, "model-protocol-public-")
	if err != nil {
		t.Fatal("cannot create controlled evidence directory")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal("cannot start native execution worker")
	}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("native execution worker did not stop")
		}
	}()
	token := uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{{OrganizationID: "test", ProjectID: h.tenant, SubjectKind: "service_account", SubjectID: "owner", TokenSHA256: device.HashCredential(token), TenantID: h.tenant}})
	if err != nil {
		t.Fatal("cannot create fixture authenticator")
	}
	providerRevision := uuid.New()
	handler, err := api.NewHandler(h.s, auth, options.Engine, api.WithExecution(worker), api.WithExecutionPolicy(h.d.Policy), api.WithModelProviderDefaults(func(context.Context, string) (*store.DeploymentModelProviderSnapshot, error) {
		return &store.DeploymentModelProviderSnapshot{Model: options.Model, HarnessConfig: options.HarnessConfig, Provider: &options.Provider, Revision: providerRevision}, nil
	}))
	if err != nil {
		t.Fatal("cannot create public API handler")
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	stop := startNativeEngineDaemon(t, h, home, binary, options.Engine)
	defer func() { stop() }()
	evidence := filepath.Join(home, "public.json")
	run := func(stage string) {
		command := exec.CommandContext(ctx, python, "../../tests/official_model_protocol_native.py", server.URL, token, optionsFile, stage, evidence)
		// Exceptions, SDK HTTP bodies and daemon diagnostics must never be echoed
		// into the test log. The script writes only allowlisted proof summaries.
		command.Stdout, command.Stderr = io.Discard, io.Discard
		if command.Run() != nil {
			t.Fatalf("native public model protocol %s failed; controlled evidence: %s", stage, evidence)
		}
	}
	run("initial")
	var proof struct {
		Session string `json:"session"`
		Calls   []struct {
			Turn    string `json:"turn"`
			Call    string `json:"call"`
			Success bool   `json:"success"`
		} `json:"calls"`
	}
	raw, err = os.ReadFile(evidence)
	if err != nil || json.Unmarshal(raw, &proof) != nil || proof.Session == "" {
		t.Fatal("missing controlled public proof")
	}
	expectedCalls := 3
	if options.Engine == "mcode" {
		expectedCalls = 0
	}
	if len(proof.Calls) != expectedCalls {
		t.Fatal("unexpected public function call count")
	}
	failed := 0
	for _, item := range proof.Calls {
		call, err := h.s.GetFunctionCall(ctx, h.tenant, proof.Session, item.Turn, item.Call)
		if err != nil || !call.Applied {
			t.Fatal("public function result lacks native delivery acknowledgement")
		}
		inputs, err := h.s.ListTurnInputs(ctx, h.tenant, proof.Session, item.Turn, 0, 100)
		if err != nil || len(inputs) != 2 || inputs[0].Kind != "message" || inputs[1].Kind != "tool_result" {
			t.Fatal("public function result input was lost or duplicated")
		}
		if !item.Success {
			failed++
		}
	}
	if expectedCalls > 0 && failed != 1 {
		t.Fatal("missing failed function-result scenario")
	}
	before, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || before.NativeSessionID == "" {
		t.Fatal("native Session binding missing before restart")
	}
	stop()
	stop = startNativeEngineDaemon(t, h, home, binary, options.Engine)
	run("resume")
	after, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || before.NativeSessionID != after.NativeSessionID {
		t.Fatal("cold Session continuation changed native history")
	}
	t.Logf("Native public model protocol acceptance passed (engine=%s protocol=%s); controlled evidence: %s", options.Engine, options.Provider.Protocol, evidence)
}
