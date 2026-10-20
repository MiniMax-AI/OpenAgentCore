package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestExecutionOptionsInheritUserEnvironment(t *testing.T) {
	r := testRequest(t)
	t.Setenv("OAC_TEST_SECRET_CANARY", "secret")
	t.Setenv("NODE_OPTIONS", "--import=untrusted")
	opts, err := prepareOptions(testStateRoot(t), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(opts.Env, "\n"), "OAC_TEST_SECRET_CANARY=secret") {
		t.Fatal("user environment lost")
	}

	data, err := os.ReadFile(filepath.Join(opts.DataDir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if json.Unmarshal(data, &config) != nil {
		t.Fatal("bad config")
	}
	features := config["agents"].(map[string]any)["default"].(map[string]any)["features"].(map[string]any)
	for _, k := range []string{"mavis", "delegation", "webSearch"} {
		if features[k] != false {
			t.Fatalf("%s remains enabled", k)
		}
	}
}

func TestExecutionRejectsUnqualifiedAuthority(t *testing.T) {
	for _, change := range []func(*proto.PromptRequestPayload){
		func(r *proto.PromptRequestPayload) { r.DisableExecutionEnvironment = false },
		func(r *proto.PromptRequestPayload) { r.DisableSubagents = false },
		func(r *proto.PromptRequestPayload) { r.RequireExistingNativeSession = true },
		func(r *proto.PromptRequestPayload) {
			r.ExecutionControls.WebSearch = &proto.WebSearchControl{Mode: "live", ContextSize: "medium"}
		},
	} {
		r := testRequest(t)
		change(&r)
		if _, err := prepareOptions(testStateRoot(t), r, nil); err == nil {
			t.Fatal("unsupported execution accepted")
		}
	}
}

func TestPublicTextDoesNotInvokeACPCommands(t *testing.T) {
	for text, want := range map[string]int{"/model": 2, "/compact": 2, "hello": 1} {
		blocks := promptContent(text)
		if len(blocks) != want || blocks[0]["text"] != text || (want == 2 && blocks[1]["text"] != "") {
			t.Fatal(blocks)
		}
	}
}
