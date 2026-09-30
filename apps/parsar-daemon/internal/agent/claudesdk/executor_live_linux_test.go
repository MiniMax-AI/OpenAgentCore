//go:build linux

package claudesdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// Explicit opt-in acceptance; normal test runs never contact a provider.
func TestLiveClaudeExecutorReuseAndCancel(t *testing.T) {
	entry, keyPath, proof := os.Getenv("OAC_TEST_CLAUDE_EXECUTOR_ENTRYPOINT"), os.Getenv("OAC_TEST_CLAUDE_EXECUTOR_KEY_FILE"), os.Getenv("OAC_TEST_CLAUDE_EXECUTOR_PROOF_DIR")
	if entry == "" || keyPath == "" || proof == "" {
		t.Skip("explicit installed runtime, private key file and proof directory required")
	}
	endpoint, model := os.Getenv("OAC_TEST_CLAUDE_EXECUTOR_BASE_URL"), os.Getenv("OAC_TEST_CLAUDE_EXECUTOR_MODEL")
	if endpoint == "" || model == "" {
		t.Fatal("explicit provider endpoint and model required")
	}
	if !filepath.IsAbs(proof) {
		t.Fatal("absolute proof directory required")
	}
	if err := os.MkdirAll(proof, 0700); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal("cannot read selected credential file")
	}
	t.Setenv("OAC_RUNTIME_HOME", proof)
	config := Config{Node: os.Getenv("OAC_TEST_CLAUDE_EXECUTOR_NODE"), Entrypoint: entry, StateDir: filepath.Join(proof, "state"), Env: []string{
		"ANTHROPIC_BASE_URL=" + endpoint, "ANTHROPIC_AUTH_TOKEN=" + strings.TrimSpace(string(key)), "ANTHROPIC_API_KEY=", "CLAUDE_CODE_OAUTH_TOKEN=", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=" + model, "ANTHROPIC_DEFAULT_OPUS_MODEL=" + model, "ANTHROPIC_DEFAULT_HAIKU_MODEL=" + model,
	}}
	if proxy := os.Getenv("OAC_TEST_CLAUDE_EXECUTOR_HTTP_PROXY"); proxy != "" {
		config.Env = append(config.Env, "HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy, "http_proxy="+proxy, "https_proxy="+proxy)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	readiness, err := CheckRuntime(ctx, config)
	if err != nil {
		t.Fatal("installed runtime readiness failed", err)
	}
	type turnEvidence struct {
		Run               string               `json:"run"`
		NodePID           int                  `json:"node_pid"`
		NativePIDs        []int                `json:"native_pids"`
		FirstTextMS       int64                `json:"first_text_ms"`
		ElapsedMS         int64                `json:"elapsed_ms"`
		CancelMS          int64                `json:"cancel_ms,omitempty"`
		Cancelled         bool                 `json:"cancelled"`
		CancellationError string               `json:"cancellation_error,omitempty"`
		Settlement        agent.TurnSettlement `json:"settlement"`
		SettlementError   string               `json:"settlement_error,omitempty"`
		Done              proto.DonePayload    `json:"done"`
		Errors            []string             `json:"errors,omitempty"`
	}
	evidence := struct {
		Runtime   RuntimeInfo    `json:"runtime"`
		PrepareMS int64          `json:"prepare_ms"`
		Turns     []turnEvidence `json:"turns"`
		Recovered bool           `json:"recovered_after_cancel"`
		Closed    bool           `json:"closed"`
	}{Runtime: readiness}
	persist := func() {
		raw, _ := json.MarshalIndent(evidence, "", "  ")
		_ = os.WriteFile(filepath.Join(proof, "executor-evidence.json"), raw, 0600)
	}
	defer persist()
	request := proto.PromptRequestPayload{StrictResume: true, DisableExecutionEnvironment: true, DisableSubagents: true, ObserveMessages: true, ExecutionControls: &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}, AgentOptions: map[string]any{"model": model, "system_prompt": "Follow requested formats briefly. Remember the exact verification marker across the conversation. Use no tools."}}
	factory := NewExecutorFactory(config)
	prepared := time.Now()
	owner, err := factory(ctx, request)
	if err != nil {
		t.Fatal("executor preparation failed", err)
	}
	evidence.PrepareMS = time.Since(prepared).Milliseconds()
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		evidence.Closed = owner.Close(closeCtx) == nil
		persist()
	}()
	nativeChildren := func(pid int) []int {
		raw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
		var ids []int
		for _, value := range strings.Fields(string(raw)) {
			child, _ := strconv.Atoi(value)
			args, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", child))
			if bytes.Contains(args, []byte("\x00--input-format\x00stream-json\x00")) && bytes.Contains(args, []byte("\x00--output-format\x00stream-json\x00")) {
				ids = append(ids, child)
			}
		}
		slices.Sort(ids)
		return ids
	}
	run := func(id, prompt string, cancelOnText bool) turnEvidence {
		t.Helper()
		started := time.Now()
		record := turnEvidence{Run: id, NodePID: owner.(*executor).base.process.Cmd.Process.Pid}
		output := make(chan proto.Envelope, 128)
		turn, err := owner.StartTurn(ctx, id, proto.TextInput(prompt), output)
		if err != nil || turn == nil {
			t.Fatal("Turn start failed", err)
		}
		cancellation := make(chan error, 1)
		var cancelAt time.Time
		for event := range output {
			if event.ID != id {
				t.Fatal("output crossed Turn identity")
			}
			if event.Type == proto.TypeDelta && record.FirstTextMS == 0 {
				record.FirstTextMS = time.Since(started).Milliseconds()
				record.NativePIDs = nativeChildren(record.NodePID)
				if cancelOnText {
					record.Cancelled = true
					cancelAt = time.Now()
					go func() {
						cancelCtx, stop := context.WithTimeout(ctx, 30*time.Second)
						defer stop()
						cancellation <- turn.Cancel(cancelCtx)
					}()
				}
			}
			if event.Type == proto.TypeError {
				var failure proto.ErrorPayload
				_ = event.DecodePayload(&failure)
				record.Errors = append(record.Errors, failure.Error)
			}
			if event.Type == proto.TypeDone {
				_ = event.DecodePayload(&record.Done)
			}
		}
		if record.Cancelled {
			err := <-cancellation
			record.CancelMS = time.Since(cancelAt).Milliseconds()
			if err != nil {
				record.CancellationError = err.Error()
			}
		}
		record.Settlement, err = turn.AwaitSettlement(ctx)
		if err != nil {
			record.SettlementError = err.Error()
		}
		record.ElapsedMS = time.Since(started).Milliseconds()
		evidence.Turns = append(evidence.Turns, record)
		persist()
		t.Logf("turn=%s bridge_pid=%d native_pids=%v reusable=%t first_text_ms=%d total_ms=%d", id, record.NodePID, record.NativePIDs, record.Settlement.Reusable, record.FirstTextMS, record.ElapsedMS)
		return record
	}
	marker := "REUSE-" + uuid.NewString()
	first := run("first", "Remember this marker: "+marker+". Reply with exactly the marker.", false)
	if first.SettlementError != "" || !first.Settlement.Reusable || !strings.Contains(first.Done.Content, marker) {
		t.Fatal("first Turn failed or was not reusable")
	}
	second := run("second", "What exact marker did I give you? Reply with only that marker.", false)
	if second.SettlementError != "" || !second.Settlement.Reusable || !strings.Contains(second.Done.Content, marker) || first.NodePID != second.NodePID || len(first.NativePIDs) == 0 || !slices.Equal(first.NativePIDs, second.NativePIDs) {
		t.Fatal("ordinary Turns did not retain native execution and history")
	}
	interrupted := run("cancel", "List the numbers 1 through 10000, one number per line, without stopping early.", true)
	if !interrupted.Cancelled {
		t.Fatal("native output ended before cancellation boundary")
	}
	if interrupted.SettlementError != "" || !interrupted.Settlement.Reusable {
		if interrupted.Settlement.Reason == "" && interrupted.SettlementError == "" {
			t.Fatal("invalid executor omitted its reason")
		}
		if err := owner.Close(ctx); err != nil {
			t.Fatal("invalid owner cleanup was not confirmed", err)
		}
		native, _ := interrupted.Done.Metadata[proto.DoneMetaAgentSessionID].(string)
		if native == "" {
			native, _ = second.Done.Metadata[proto.DoneMetaAgentSessionID].(string)
		}
		request.AgentSessionID = native
		request.RequireExistingNativeSession = true
		owner, err = factory(ctx, request)
		if err != nil {
			t.Fatal("history recovery failed", err)
		}
		evidence.Recovered = true
	}
	continued := run("continued", "What exact marker did I originally give you? Reply with only the marker.", false)
	if continued.SettlementError != "" || !continued.Settlement.Reusable || !strings.Contains(continued.Done.Content, marker) {
		t.Fatal("history did not continue after cancellation")
	}
	if !evidence.Recovered && (continued.NodePID != second.NodePID || !slices.Equal(continued.NativePIDs, second.NativePIDs)) {
		t.Fatal("reusable cancellation replaced native execution")
	}
}
