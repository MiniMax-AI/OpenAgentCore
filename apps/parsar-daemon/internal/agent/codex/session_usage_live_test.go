package codex

import (
	"context"
	"encoding/json"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const activeUsageNotification = `{"method":"thread/tokenUsage/updated","params":{"threadId":"thread","turnId":"turn","tokenUsage":{"total":{"inputTokens":30,"cachedInputTokens":4,"outputTokens":10,"reasoningOutputTokens":2,"totalTokens":40}}}}`

func TestUsagePublishedBeforeCompletion(t *testing.T) {
	out := make(chan proto.Envelope, 8)
	s := &Session{runID: "run", out: out, cancelCtx: t.Context(), cfg: defaultSessionConfig()}
	s.setThreadID("thread")
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","turnId":"old","tokenUsage":{"total":{"inputTokens":10,"cachedInputTokens":1,"outputTokens":3,"reasoningOutputTokens":1,"totalTokens":13}}}`))
	if len(s.out) != 0 {
		t.Fatal("resume baseline was published as current usage")
	}
	s.onTurnStarted(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn"}}`))
	var notification struct {
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal([]byte(activeUsageNotification), &notification); err != nil {
		t.Fatal(err)
	}
	s.onUsageUpdated(notification.Params)
	s.onUsageUpdated(notification.Params)
	if s.terminal.Load() || len(s.out) != 2 {
		t.Fatalf("expected repeated live snapshots before completion, got %d", len(s.out))
	}
	var payload proto.UsagePayload
	event := <-out
	if err := event.DecodePayload(&payload); err != nil {
		t.Fatal(err)
	}
	want := proto.TokenUsage{InputTokens: 20, CachedInputTokens: 3, OutputTokens: 7, ReasoningOutputTokens: 1, TotalTokens: 27}
	if event.Type != proto.TypeUsage || event.ID != "run" || payload.Tokens == nil || *payload.Tokens != want {
		t.Fatalf("wrong live snapshot: %+v %+v", event, payload)
	}
	repeated := <-out
	if repeated.Type != proto.TypeUsage || string(repeated.Payload) != string(event.Payload) {
		t.Fatal("repeated cumulative snapshot changed the reported measurement")
	}
	// No terminal event is needed to observe or retain the current measurement.
	if got := s.CancellationOutcome().Usage.Tokens; got == nil || *got != want {
		t.Fatalf("live observation missing from cancellation outcome: %+v", got)
	}
}

func TestUsageBackpressureKeepsSnapshotReadableAndCompletionOrdered(t *testing.T) {
	s, _, server := cancellationTestSession(t)
	out := make(chan proto.Envelope, 1)
	s.out = out
	s.out <- proto.Envelope{Type: "occupied"}
	s.registerHandlers()
	s.setThreadID("thread")
	s.onTurnStarted(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn"}}`))
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(server.ToClient, activeUsageNotification+"\n"+`{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn","status":"completed"}}}`+"\n")
		written <- err
	}()
	waitForUsageBackpressure(t, s)
	assertReadableUsageSnapshot(t, s)
	if s.terminal.Load() {
		t.Fatal("completion overtook the blocked live usage")
	}
	<-out
	for _, kind := range []string{proto.TypeUsage, proto.TypeUsage, proto.TypeDone} {
		select {
		case event := <-out:
			if event.Type != kind {
				t.Fatalf("event %q, want %q", event.Type, kind)
			}
			if kind == proto.TypeDone {
				var done proto.DonePayload
				if err := event.DecodePayload(&done); err != nil || done.Usage.Tokens == nil || done.Usage.Tokens.TotalTokens != 40 {
					t.Fatalf("completion lost its snapshot: %+v %v", done, err)
				}
			}
		case <-time.After(time.Second):
			t.Fatal("ordered native output did not resume")
		}
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}

func TestCancellationReleasesUsageBackpressure(t *testing.T) {
	s, _, server := cancellationTestSession(t)
	s.out = make(chan proto.Envelope, 1)
	s.out <- proto.Envelope{Type: "occupied"}
	s.registerHandlers()
	s.setThreadID("thread")
	s.onTurnStarted(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn"}}`))
	requests := collectCancellationRequests(t, server, "response timeout")
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(server.ToClient, activeUsageNotification+"\n")
		written <- err
	}()
	waitForUsageBackpressure(t, s)
	assertReadableUsageSnapshot(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	if err := s.Cancel(ctx); err != nil {
		t.Fatal("cancellation could not release usage backpressure", err)
	}
	if s.cancelCtx.Err() == nil || len(<-requests) != 1 {
		t.Fatal("native cancellation did not settle")
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	assertReadableUsageSnapshot(t, s)
}

func waitForUsageBackpressure(t *testing.T, s *Session) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for s.outMu.TryLock() {
		s.outMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("native usage did not reach the blocked output")
		}
		runtime.Gosched()
	}
}

func assertReadableUsageSnapshot(t *testing.T, s *Session) {
	t.Helper()
	read := make(chan proto.DonePayload, 1)
	go func() { read <- s.CancellationOutcome() }()
	select {
	case got := <-read:
		if got.Usage.Tokens == nil || got.Usage.Tokens.TotalTokens != 40 {
			t.Fatalf("missing observed snapshot: %+v", got.Usage)
		}
	case <-time.After(time.Second):
		t.Fatal("output backpressure held the usage snapshot lock")
	}
}

func TestLiveUsageRequiresConsistentCompleteBreakdown(t *testing.T) {
	for name, counters := range map[string]string{
		"missing":   `{"inputTokens":5,"outputTokens":2}`,
		"null":      `{"inputTokens":5,"cachedInputTokens":0,"outputTokens":2,"reasoningOutputTokens":null,"totalTokens":7}`,
		"negative":  `{"inputTokens":5,"cachedInputTokens":-1,"outputTokens":2,"reasoningOutputTokens":0,"totalTokens":7}`,
		"total":     `{"inputTokens":5,"cachedInputTokens":0,"outputTokens":2,"reasoningOutputTokens":0,"totalTokens":8}`,
		"cache":     `{"inputTokens":5,"cachedInputTokens":6,"outputTokens":2,"reasoningOutputTokens":0,"totalTokens":7}`,
		"reasoning": `{"inputTokens":5,"cachedInputTokens":0,"outputTokens":2,"reasoningOutputTokens":3,"totalTokens":7}`,
		"large sum": `{"inputTokens":9223372036854775807,"cachedInputTokens":0,"outputTokens":1,"reasoningOutputTokens":0,"totalTokens":9223372036854775807}`,
	} {
		t.Run(name, func(t *testing.T) {
			out := make(chan proto.Envelope, 1)
			s := &Session{out: out, cancelCtx: t.Context(), cfg: defaultSessionConfig()}
			s.setThreadID("thread")
			s.onTurnStarted(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn"}}`))
			s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","turnId":"turn","tokenUsage":{"total":` + counters + `}}`))
			if len(s.out) != 1 {
				t.Fatal("native snapshot was not published")
			}
			var usage proto.UsagePayload
			event := <-out
			if err := event.DecodePayload(&usage); err != nil || usage.Tokens != nil {
				t.Fatalf("incomplete native snapshot became public token usage: %+v %v", usage, err)
			}
		})
	}
}

func TestLiveUsageRejectsInconsistentResumeDelta(t *testing.T) {
	out := make(chan proto.Envelope, 2)
	s := &Session{out: out, cancelCtx: t.Context(), cfg: defaultSessionConfig()}
	s.setThreadID("thread")
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","turnId":"old","tokenUsage":{"total":{"inputTokens":100,"cachedInputTokens":90,"outputTokens":50,"reasoningOutputTokens":10,"totalTokens":150}}}`))
	s.onTurnStarted(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn"}}`))
	// Both thread snapshots are complete, but cache growth exceeds input growth.
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","turnId":"turn","tokenUsage":{"total":{"inputTokens":110,"cachedInputTokens":105,"outputTokens":55,"reasoningOutputTokens":11,"totalTokens":165}}}`))
	var usage proto.UsagePayload
	event := <-out
	if err := event.DecodePayload(&usage); err != nil || usage.Tokens != nil {
		t.Fatalf("inconsistent per-Turn delta became public token usage: %+v %v", usage, err)
	}
	s.onTurnCompleted(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`))
	previous := *s.latestUsage
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","turnId":"turn","tokenUsage":{"total":{"inputTokens":130,"cachedInputTokens":105,"outputTokens":60,"reasoningOutputTokens":12,"totalTokens":190}}}`))
	if *s.latestUsage != previous {
		t.Fatal("late usage changed the completed Turn")
	}
}
