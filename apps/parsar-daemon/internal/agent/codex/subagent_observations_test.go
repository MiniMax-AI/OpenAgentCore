package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type subagentFixture struct {
	mu            sync.Mutex
	home          string
	childStatus   string
	interrupted   int
	interruptGate <-chan struct{}
	childItems    []json.RawMessage
}

func (f *subagentFixture) history(id string) subagentHistory {
	h := subagentHistory{Thread: Thread{ID: id, Cwd: "/workspace", CreatedAt: 100, Path: filepath.Join(f.home, "sessions", id+".jsonl"), Status: map[string]string{"type": "idle"}}, HistoryMode: "paginated"}
	start, finish := int64(101), int64(103)
	turn := subagentNativeTurn{ID: id + "-turn", Status: "completed", StartedAt: &start, CompletedAt: &finish, ItemsView: "full"}
	if id == "root" {
		turn.Items = []json.RawMessage{json.RawMessage(`{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","status":"completed","senderThreadId":"root","receiverThreadIds":["child"],"prompt":"real child prompt"}`)}
	} else {
		h.Parent = "root"
		turn.Status = f.childStatus
		turn.Items = f.childItems
		if turn.Status == "inProgress" {
			turn.CompletedAt = nil
			h.Status = map[string]string{"type": "active"}
		}
	}
	h.Turns = []subagentNativeTurn{turn}
	return h
}

func (f *subagentFixture) persist(t *testing.T) {
	t.Helper()
	for _, id := range []string{"root", "child"} {
		h := f.history(id)
		rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": id, "cwd": h.Cwd}}}
		for _, item := range h.Turns[0].Items {
			var v map[string]any
			_ = json.Unmarshal(item, &v)
			rows = append(rows, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": id, "turn_id": id + "-turn", "completed_at_ms": 102000, "item": map[string]any{"id": v["id"], "type": "AgentMessage"}}})
		}
		var body []byte
		for _, row := range rows {
			line, _ := json.Marshal(row)
			body = append(append(body, line...), '\n')
		}
		if err := os.WriteFile(h.Path+".tmp", body, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(h.Path+".tmp", h.Path); err != nil {
			t.Fatal(err)
		}
	}
}

func observationSession(t *testing.T, status string) (*Session, *subagentFixture, <-chan proto.Envelope) {
	t.Helper()
	client, server, cleanup := NewTestClient()
	ctx, cancel := context.WithCancel(t.Context())
	out := make(chan proto.Envelope, 128)
	f := &subagentFixture{home: t.TempDir(), childStatus: status, childItems: []json.RawMessage{json.RawMessage(`{"type":"agentMessage","id":"answer","text":"child result"}`)}}
	if err := os.Mkdir(filepath.Join(f.home, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	f.persist(t)
	s := &Session{runID: "run", nativeHome: f.home, rpc: client.JSONRPCClient, out: out, cancelCtx: ctx, cancelFn: cancel, cfg: defaultSessionConfig(), bufs: NewItemBuffers(), interactions: newPendingCodexInteractions()}
	s.setThreadID("root")
	s.beginRootTurn("root", "root-turn")
	s.startSubagentObservations()
	s.registerHandlers()
	go func() {
		decoder := json.NewDecoder(server.FromClient)
		for {
			var request JsonRpcRequest
			if decoder.Decode(&request) != nil {
				return
			}
			args, _ := json.Marshal(request.Params)
			var p map[string]any
			_ = json.Unmarshal(args, &p)
			f.mu.Lock()
			id, _ := p["threadId"].(string)
			var result any
			switch request.Method {
			case "thread/read":
				result = map[string]any{"thread": f.history(id)}
			case "thread/turns/list":
				result = map[string]any{"data": f.history(id).Turns, "nextCursor": nil}
			case "thread/list":
				result = map[string]any{"data": []any{map[string]any{"id": "child", "parentThreadId": "root", "createdAt": 100, "agentNickname": "Child", "source": map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "root"}}}}}, "nextCursor": nil}
			case "turn/interrupt":
				gate := f.interruptGate
				f.mu.Unlock()
				if gate != nil {
					<-gate
				}
				f.mu.Lock()
				f.interrupted++
				f.childStatus = "interrupted"
				f.persist(t)
				result = map[string]any{}
			default:
				result = map[string]any{}
			}
			f.mu.Unlock()
			if json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": result}) != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		cleanup()
		select {
		case <-s.subagents.done:
		case <-time.After(time.Second):
			t.Error("observer outlived owner")
		}
	})
	return s, f, out
}

func collectObserved(t *testing.T, out <-chan proto.Envelope) []proto.Envelope {
	t.Helper()
	var all []proto.Envelope
	for {
		select {
		case event, open := <-out:
			if !open {
				return all
			}
			all = append(all, event)
		case <-time.After(5 * time.Second):
			t.Fatal("observation did not settle")
			return nil
		}
	}
}

func TestSubagentFactsPrecedeFrozenRootTerminal(t *testing.T) {
	s, _, out := observationSession(t, "completed")
	s.onTurnCompleted(json.RawMessage(`{"threadId":"root","turn":{"id":"root-turn","status":"completed","completedAt":102}}`))
	events := collectObserved(t, out)
	kinds := []string{proto.TypeSubagentIdentity, proto.TypeSubagentCoordination, proto.TypeSubagentTurn, proto.TypeSubagentItem, proto.TypeSubagentTurn, proto.TypeDone}
	if len(events) != len(kinds) {
		t.Fatalf("unexpected observations: %#v", events)
	}
	for i, event := range events {
		if event.Type != kinds[i] {
			t.Fatalf("event %d: %s", i, event.Type)
		}
	}
	var identity proto.SubagentIdentityPayload
	_ = events[0].DecodePayload(&identity)
	if identity.Instructions == nil || *identity.Instructions != "real child prompt" || identity.Name == nil || *identity.Name != "Child" {
		t.Fatal(identity)
	}
	var turn proto.SubagentTurnPayload
	_ = events[4].DecodePayload(&turn)
	if turn.Status != "completed" || turn.CompletedAtMS == nil || *turn.CompletedAtMS != 103000 || turn.Usage != nil {
		t.Fatal(turn)
	}
	var done proto.DonePayload
	_ = events[5].DecodePayload(&done)
	if done.SourceCompletedAtMS == nil || *done.SourceCompletedAtMS != 102000 {
		t.Fatal(done)
	}
}

func TestSubagentRootFirstRetainsChildUntilTerminal(t *testing.T) {
	s, f, out := observationSession(t, "inProgress")
	s.emitDone("frozen", nil)
	for i := 0; i < 4; i++ {
		select {
		case e := <-out:
			if e.Type == proto.TypeDone {
				t.Fatal("root released active child")
			}
		case <-time.After(time.Second):
			t.Fatal("child facts missing")
		}
	}
	select {
	case e := <-out:
		t.Fatalf("unexpected terminal: %s", e.Type)
	case <-time.After(20 * time.Millisecond):
	}
	f.mu.Lock()
	f.childStatus = "completed"
	f.persist(t)
	f.mu.Unlock()
	events := collectObserved(t, out)
	if events[len(events)-1].Type != proto.TypeDone {
		t.Fatal(events)
	}
}

func TestSubagentCancellationAfterRootFrozenCollectsNativeTerminal(t *testing.T) {
	s, f, out := observationSession(t, "inProgress")
	s.emitDone("frozen", nil)
	for i := 0; i < 4; i++ {
		select {
		case <-out:
		case <-time.After(time.Second):
			t.Fatal("child not active")
		}
	}
	if err := s.Cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	events := collectObserved(t, out)
	var cancelled bool
	for _, event := range events {
		if event.Type == proto.TypeSubagentTurn {
			var turn proto.SubagentTurnPayload
			_ = event.DecodePayload(&turn)
			cancelled = turn.Status == "cancelled"
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.interrupted != 1 || !cancelled {
		t.Fatalf("interrupts=%d cancelled=%v", f.interrupted, cancelled)
	}
}
