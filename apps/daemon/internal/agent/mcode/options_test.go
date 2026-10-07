package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestOptionsRefreshManagedState(t *testing.T) {
	t.Setenv("MINIMAX_DATA_DIR", "/wrong")
	req := testRequest(t)
	opts, err := prepareOptions(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(opts.Dir, os.Getenv("OAC_RUNTIME_HOME")+string(os.PathSeparator)) {
		t.Fatalf("workdir escaped managed state: %s", opts.Dir)
	}
	dataDir := ""
	for _, entry := range opts.Env {
		if value, ok := strings.CutPrefix(entry, "MINIMAX_DATA_DIR="); ok {
			dataDir = value
		}
	}
	if dataDir != opts.DataDir {
		t.Fatal("state override did not win")
	}
	req.AgentOptions["system_prompt"] = ""
	req.AgentSessionID = "native-1"
	refreshed, err := prepareOptions(req)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.DataDir != opts.DataDir {
		t.Fatal("resume moved native state")
	}
	content, err := os.ReadFile(filepath.Join(opts.DataDir, "AGENTS.md"))
	if err != nil || len(content) != 0 {
		t.Fatal("removed instructions retained on resume")
	}
	data, err := os.ReadFile(filepath.Join(opts.DataDir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if json.Unmarshal(data, &cfg) != nil {
		t.Fatal("invalid config")
	}
	if cfg["permissionMode"] != "auto" {
		t.Fatal("native permission mode changed")
	}
	if cfg["skills"].(map[string]any)["external"].(map[string]any)["enabled"] != false {
		t.Fatal("external discovery enabled")
	}
	info, _ := os.Stat(filepath.Join(opts.DataDir, "config.yaml"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("native credentials file is not private")
	}
}

func TestOptionsRejectDroppedContext(t *testing.T) {
	tests := []struct {
		name string
		edit func(*proto.PromptRequestPayload)
	}{
		{"oversized instructions", func(r *proto.PromptRequestPayload) { r.AgentOptions["system_prompt"] = strings.Repeat("x", 32*1024+1) }},
		{"attachment", func(r *proto.PromptRequestPayload) {
			r.Input = proto.MessageInput{{Content: []proto.InputContent{{Type: "input_image"}}}}
		}},
		{"missing model", func(r *proto.PromptRequestPayload) { delete(r.AgentOptions, "model") }},
		{"missing provider", func(r *proto.PromptRequestPayload) { delete(r.AgentOptions, "model_provider") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testRequest(t)
			tt.edit(&req)
			if _, err := prepareOptions(req); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestQuestionContentPreservesTypesAndValidates(t *testing.T) {
	pending := pendingQuestion{Properties: map[string]formProperty{"text": {Type: "string"}, "choice": {Type: "string", Options: []formOption{{Value: "eu", Title: "Europe"}}}}, Required: []string{"choice"}}
	if _, err := questionContent(pending, proto.PromptForUserChoiceDecisionPayload{}); err == nil {
		t.Fatal("required answer accepted empty")
	}
	decision := proto.PromptForUserChoiceDecisionPayload{QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{{QuestionID: "text", Answers: []string{"custom"}}, {QuestionID: "choice", Answers: []string{"Europe"}}}}
	content, err := questionContent(pending, decision)
	if err != nil {
		t.Fatal(err)
	}
	if content["choice"] != "eu" || content["text"] != "custom" {
		t.Fatalf("answers=%v", content)
	}
	decision.QuestionAnswers[1].Answers = []string{"unoffered"}
	if _, err := questionContent(pending, decision); err == nil {
		t.Fatal("unoffered choice accepted")
	}
}
