package store_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativePublicFunctionStreamHelper(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	h, ctx, home := nativeDispatchHarness(t)
	// Codex sends a result made of one input_text item as a plain string output.
	model, requests := nativeFunctionResultsModel(t, home, []any{
		"setup complete",
		`{"ticket":"42","status":"open"}`,
		"Tool handler failed.",
	})
	defer model.Close()
	serverURL, token := nativePublicFunctionServer(t, h, ctx, nativeModelProvider(model))
	proofPath := filepath.Join(home, "public-function-stream.json")
	command := exec.CommandContext(ctx, python, "../../tests/official_function_stream.py", serverURL, token, proofPath)
	if log, err := command.CombinedOutput(); err != nil {
		t.Fatalf("official native stream helper: %v %s", err, log)
	}
	var proof struct {
		Session string   `json:"session"`
		Turns   []string `json:"turns"`
		Calls   []string `json:"calls"`
	}
	raw, err := os.ReadFile(proofPath)
	if err != nil || json.Unmarshal(raw, &proof) != nil || len(proof.Turns) != 2 || len(proof.Calls) != 2 {
		t.Fatal(proof, err)
	}
	for i, callID := range proof.Calls {
		call, err := h.s.GetFunctionCall(ctx, h.tenant, proof.Session, proof.Turns[i], callID)
		if err != nil || !call.Applied {
			t.Fatal(call, err)
		}
	}
	bound, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || bound.NativeSessionID == "" || bound.Device.ID != h.device.ID {
		t.Fatal(bound, err)
	}
	if requests.Load() != 6 {
		t.Fatal("unexpected replay or missing native continuation", requests.Load())
	}
	t.Logf("Official SDK stream tool handlers, error omission, public history and native application passed; evidence %s", home)
}
