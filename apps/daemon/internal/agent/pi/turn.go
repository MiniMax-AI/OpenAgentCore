package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Pi persists one entry per accepted message. The adapter treats the entry id
// as a durable cursor and only reports a native receipt when exactly one user
// entry appears after it; text equality is corruption detection, never identity.

// unsupported reports an extension this adapter does not serve.
func unsupported(operation string) error {
	return fmt.Errorf("%w: %s", agent.ErrUnsupportedOperation, operation)
}

type Session struct {
	runID     string
	sessionID string
	out       chan<- proto.Envelope
	rpc       *rpcProcess

	settled         chan struct{}
	once            sync.Once
	settle          agent.TurnSettlement
	err             error
	observeMessages bool

	mu        sync.Mutex
	steerMu   sync.Mutex
	failure   error
	closedOut bool
	cursor    string
	content   strings.Builder
	usage     proto.Usage
	seq       uint64
	index     int
	currentID string
	cancel    bool
}

func newSession(runID string, out chan<- proto.Envelope, rpc *rpcProcess, cursor, sessionID string, observeMessages bool) *Session {
	return &Session{runID: runID, sessionID: sessionID, out: out, rpc: rpc, cursor: cursor, observeMessages: observeMessages, settled: make(chan struct{})}
}

// entry is one session entry. Only the fields the adapter uses are decoded.
type entry struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type entriesPage struct {
	Entries []entry `json:"entries"`
	LeafID  string  `json:"leafId"`
}

// entriesLeaf returns the newest entry id, which every later cursor read starts
// from. A session without entries has no cursor.
func entriesLeaf(data json.RawMessage) string {
	var page entriesPage
	if err := json.Unmarshal(data, &page); err != nil {
		return ""
	}
	if page.LeafID != "" {
		return page.LeafID
	}
	if len(page.Entries) != 0 {
		return page.Entries[len(page.Entries)-1].ID
	}
	return ""
}

func userEntries(page entriesPage) []entry {
	var users []entry
	for _, item := range page.Entries {
		if item.Type == "message" && item.Message.Role == "user" {
			users = append(users, item)
		}
	}
	return users
}

type piUsage struct {
	Input       int32 `json:"input"`
	Output      int32 `json:"output"`
	CacheRead   int32 `json:"cacheRead"`
	CacheWrite  int32 `json:"cacheWrite"`
	TotalTokens int32 `json:"totalTokens"`
	Cost        struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

type assistantEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

type messageRecord struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (t *Session) handle(event Event) {
	switch event.Type {
	case "message_start":
		var record struct {
			Message messageRecord `json:"message"`
		}
		if err := json.Unmarshal(event.Raw, &record); err != nil || record.Message.Role != "assistant" {
			return
		}
		t.mu.Lock()
		t.index++
		t.currentID = t.runID + ":assistant:" + strconv.Itoa(t.index)
		t.mu.Unlock()
	case "message_update":
		var update struct {
			Usage                 *piUsage       `json:"usage"`
			AssistantMessageEvent assistantEvent `json:"assistantMessageEvent"`
		}
		if err := json.Unmarshal(event.Raw, &update); err != nil {
			return
		}
		t.mu.Lock()
		item := t.currentID
		t.mu.Unlock()
		switch update.AssistantMessageEvent.Type {
		case "text_delta":
			t.emit(proto.TypeDelta, proto.DeltaPayload{ItemID: item, Delta: update.AssistantMessageEvent.Delta, Sequence: t.next()})
		case "thinking_delta":
			t.emit(proto.TypeThinking, proto.ThinkingPayload{Text: update.AssistantMessageEvent.Delta, Sequence: t.next()})
		}
		if update.Usage != nil {
			usage := convertUsage(update.Usage)
			t.mu.Lock()
			t.usage = usage
			t.mu.Unlock()
			t.emit(proto.TypeUsage, proto.UsagePayload{Usage: usage})
		}
	case "message_end":
		var record struct {
			Message struct {
				Role         string          `json:"role"`
				Content      json.RawMessage `json:"content"`
				StopReason   string          `json:"stopReason"`
				ErrorMessage string          `json:"errorMessage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(event.Raw, &record); err != nil || record.Message.Role != "assistant" {
			return
		}
		if reason := record.Message.StopReason; reason == "error" || reason == "aborted" {
			t.mu.Lock()
			cancelled := t.cancel
			t.mu.Unlock()
			if reason == "error" || !cancelled {
				message := record.Message.ErrorMessage
				if message == "" {
					message = "model response ended with " + reason
				}
				t.fail(fmt.Errorf("pi: %s", message))
			}
			return
		}
		text := messageText(record.Message.Content)
		t.mu.Lock()
		t.content.WriteString(text)
		id := t.currentID
		if id == "" {
			t.index++
			id = t.runID + ":assistant:" + strconv.Itoa(t.index)
		}
		observe := t.observeMessages
		t.mu.Unlock()
		// Message boundaries are opt-in observations. Emitting a completed
		// message without the request duplicates the streamed text downstream.
		if observe {
			t.emit(proto.TypeOutputMessage, proto.OutputMessagePayload{ID: id, Status: "completed", Text: &text})
		}
	case "tool_execution_start", "tool_execution_end":
		var call struct {
			ToolCallID string         `json:"toolCallId"`
			ToolName   string         `json:"toolName"`
			Args       map[string]any `json:"args"`
			Result     map[string]any `json:"result"`
		}
		if err := json.Unmarshal(event.Raw, &call); err != nil {
			return
		}
		stage := "before"
		if event.Type == "tool_execution_end" {
			stage = "after"
		}
		t.emit(proto.TypeToolCall, proto.ToolCallPayload{ID: call.ToolCallID, Name: call.ToolName, Stage: stage, Args: call.Args, Result: call.Result})
	case "agent_settled":
		settled := struct {
			Aborted bool `json:"aborted"`
		}{}
		_ = json.Unmarshal(event.Raw, &settled)
		t.mu.Lock()
		failure, cancelled := t.failure, t.cancel
		t.mu.Unlock()
		switch {
		case failure != nil:
			t.fail(failure)
		case settled.Aborted && !cancelled:
			t.fail(fmt.Errorf("pi: run aborted without a cancellation request"))
		default:
			t.finish(agent.TurnSettlement{Reusable: true}, nil)
		}
	}
}

func convertUsage(usage *piUsage) proto.Usage {
	return proto.Usage{
		InputTokens:  usage.Input,
		OutputTokens: usage.Output,
		CostUSD:      usage.Cost.Total,
		Raw: map[string]any{
			"cache_read":  usage.CacheRead,
			"cache_write": usage.CacheWrite,
			"total":       usage.TotalTokens,
		},
	}
}

// messageText joins an assistant message's text blocks. Pi's completed message
// is authoritative; streamed deltas are only a live display.
func messageText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		var text strings.Builder
		for _, block := range blocks {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		return text.String()
	}
	var single string
	if err := json.Unmarshal(content, &single); err == nil {
		return single
	}
	return ""
}

func (t *Session) emit(typ string, payload any) {
	envelope, err := proto.NewEnvelope(typ, t.runID, payload)
	if err != nil {
		return
	}
	t.mu.Lock()
	if t.closedOut {
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()
	t.out <- envelope
}

func (t *Session) next() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	return t.seq
}

func (t *Session) fail(err error) {
	t.mu.Lock()
	if t.failure == nil {
		t.failure = err
	}
	t.mu.Unlock()
	t.finish(agent.TurnSettlement{Reason: err.Error()}, err)
}

// finish publishes the terminal frames once and releases the output channel.
func (t *Session) finish(settlement agent.TurnSettlement, err error) {
	t.once.Do(func() {
		t.mu.Lock()
		content := t.content.String()
		usage := t.usage
		cancelled := t.cancel
		t.mu.Unlock()
		if err != nil {
			t.emit(proto.TypeError, proto.ErrorPayload{Error: err.Error(), Code: "runtime_error"})
		} else if cancelled {
			settlement.Reusable = true
		}
		t.emit(proto.TypeDone, proto.DonePayload{Content: content, Usage: usage, Metadata: t.metadata()})
		t.mu.Lock()
		t.closedOut = true
		t.mu.Unlock()
		close(t.out)
		t.settle = settlement
		t.err = err
		close(t.settled)
	})
}

func (t *Session) metadata() map[string]any {
	return map[string]any{proto.DoneMetaAgentSessionID: t.sessionID}
}

func (t *Session) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	select {
	case <-t.settled:
		return t.settle, t.err
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

func (t *Session) Cancel(ctx context.Context) error {
	select {
	case <-t.settled:
		// A settled Turn owns nothing: aborting the process now would cancel a
		// successor Turn.
		return nil
	default:
	}
	t.mu.Lock()
	t.cancel = true
	t.mu.Unlock()
	deadline, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()
	_, _ = t.rpc.call(deadline, "clear_queue", nil)
	_, err := t.rpc.call(deadline, "abort", nil)
	if err != nil && errors.Is(err, errRPCClosed) {
		return nil
	}
	return err
}

func (t *Session) CancellationOutcome() proto.DonePayload {
	t.mu.Lock()
	defer t.mu.Unlock()
	return proto.DonePayload{Content: t.content.String(), Usage: t.usage, Metadata: t.metadata()}
}

func (t *Session) SteerWithReceipt(ctx context.Context, payload proto.PromptSteerPayload, written func()) error {
	text, ok := payloadText(payload)
	if !ok {
		return fmt.Errorf("pi: steering requires text input")
	}
	// One pending input per Turn mirrors the Runtime's serialization and Pi's
	// single in-memory queue.
	t.steerMu.Lock()
	defer t.steerMu.Unlock()
	// Consume every entry written so far (the prompt, earlier steers, assistant
	// output) so the receipt window covers only this submission.
	cursor := t.cursor
	if data, err := t.rpc.call(ctx, "get_entries", map[string]any{"since": cursor}); err == nil {
		cursor = entriesLeaf(data)
	}
	data, err := t.rpc.call(ctx, "steer", map[string]any{"message": text})
	if err != nil {
		return err
	}
	var ack struct {
		Disposition string `json:"disposition"`
	}
	_ = json.Unmarshal(data, &ack)
	if ack.Disposition == "handled" {
		return fmt.Errorf("pi: steering was consumed without a native user message")
	}
	if written != nil {
		written()
	}
	return t.awaitUserEntry(ctx, cursor, text)
}

// awaitUserEntry waits for the native application receipt: exactly one user
// entry after the cursor, with matching text. Anything else stays unknown.
func (t *Session) awaitUserEntry(ctx context.Context, cursor, text string) error {
	deadline := time.Now().Add(t.rpc.timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	for {
		data, err := t.rpc.call(ctx, "get_entries", map[string]any{"since": cursor})
		if err != nil {
			return err
		}
		var page entriesPage
		if err := json.Unmarshal(data, &page); err != nil {
			return fmt.Errorf("pi: decode entries: %w", err)
		}
		users := userEntries(page)
		switch {
		case len(users) > 1:
			return fmt.Errorf("pi: steering entry is ambiguous: %d user entries after the cursor", len(users))
		case len(users) == 1:
			if messageText(users[0].Message.Content) != text {
				return fmt.Errorf("pi: steering entry does not match the submitted input")
			}
			t.mu.Lock()
			t.cursor = entriesLeaf(data)
			t.mu.Unlock()
			return nil
		}
		if leaf := entriesLeaf(data); leaf != cursor && len(page.Entries) != 0 {
			cursor = leaf
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pi: steering application is unknown: no native user entry after the cursor")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func payloadText(payload proto.PromptSteerPayload) (string, bool) {
	text, err := payload.Input.TextOnly()
	if err != nil {
		return "", false
	}
	return text, true
}

func (t *Session) Steer(ctx context.Context, payload proto.PromptSteerPayload) error {
	text, ok := payloadText(payload)
	if !ok {
		return fmt.Errorf("pi: steering requires text input")
	}
	t.steerMu.Lock()
	defer t.steerMu.Unlock()
	data, err := t.rpc.call(ctx, "steer", map[string]any{"message": text})
	if err != nil {
		return err
	}
	var ack struct {
		Disposition string `json:"disposition"`
	}
	_ = json.Unmarshal(data, &ack)
	if ack.Disposition == "handled" {
		return fmt.Errorf("pi: steering was consumed without a native user message")
	}
	return nil
}

func (t *Session) SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error {
	return unsupported("function_result")
}

func (t *Session) ReadWorkspaceFile(context.Context, string, int) (agent.WorkspaceReadResult, error) {
	return agent.WorkspaceReadResult{}, unsupported("workspace_read")
}

func (t *Session) ListWorkspaceDirectory(context.Context, string, int) (agent.WorkspaceDirectoryResult, error) {
	return agent.WorkspaceDirectoryResult{}, unsupported("workspace_list")
}

func (t *Session) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, unsupported("workspace_write")
}
