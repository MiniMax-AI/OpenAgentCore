package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestExecutionDisablesNativeFeatures(t *testing.T) {
	opts, err := fakeInstall("node").prepare(prepared(t, testRequest(t)), hostSession(t))
	if err != nil {
		t.Fatal(err)
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
		func(r *proto.PromptRequestPayload) { r.ExecutionControls = nil },
	} {
		r := testRequest(t)
		change(&r)
		if _, err := fakeInstall("node").prepare(prepared(t, r), hostSession(t)); err == nil {
			t.Fatal("unsupported execution accepted")
		}
	}
}

func TestPublicTextDoesNotInvokeACPCommands(t *testing.T) {
	for _, text := range []string{"/model", "/compact", "hello"} {
		blocks := promptContent(text)
		if len(blocks) != 2 || blocks[0]["text"] != text || blocks[1]["text"] != "" {
			t.Fatal(blocks)
		}
	}
}
