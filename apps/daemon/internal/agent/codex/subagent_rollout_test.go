package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func lifecycleRollout(t *testing.T, change func([]map[string]any)) (string, subagentHistory) {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	h := subagentHistory{Thread: Thread{ID: "root", Cwd: "/workspace", Path: filepath.Join(home, "sessions", "root.jsonl")}, Turns: []subagentNativeTurn{{ID: "turn", Status: "completed", Items: []json.RawMessage{json.RawMessage(`{"type":"collabAgentToolCall","id":"call","tool":"closeAgent","status":"completed","senderThreadId":"root","receiverThreadIds":["child"]}`)}}}}
	rows := []map[string]any{
		{"type": "session_meta", "payload": map[string]any{"id": "root", "cwd": "/workspace"}},
		{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "close_agent", "namespace": "multi_agent_v1", "call_id": "call", "arguments": `{"target":"child"}`}},
		{"type": "event_msg", "timestamp": "2099-01-01T00:00:00Z", "payload": map[string]any{"type": "item_completed", "thread_id": "root", "turn_id": "turn", "completed_at_ms": 123456, "item": map[string]any{"type": "CollabAgentToolCall", "id": "call", "tool": "close_agent", "status": "completed", "sender_thread_id": "root", "receiver_thread_ids": []string{"child"}}}},
		{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "call", "output": `{"previous_status":{"completed":"result"}}`}},
	}
	if change != nil {
		change(rows)
	}
	var body []byte
	for _, row := range rows {
		line, _ := json.Marshal(row)
		body = append(append(body, line...), '\n')
	}
	if err := os.WriteFile(h.Path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return home, h
}

func TestSubagentLifecycleRequiresCorrelatedNativeReceipt(t *testing.T) {
	home, h := lifecycleRollout(t, nil)
	effects, err := readSubagentEffects(home, &h)
	if err != nil || len(effects) != 1 {
		t.Fatal(effects, err)
	}
	e := effects[0]
	if e.NativeID != "child" || e.Status != "closed" || e.OccurredAtMS != 123456 || e.EffectID != "root:call" {
		t.Fatal(e)
	}
	for name, change := range map[string]func([]map[string]any){
		"missing output":                     func(rows []map[string]any) { rows[3]["type"] = "other" },
		"error after completed notification": func(rows []map[string]any) { rows[3]["payload"].(map[string]any)["output"] = "native error" },
		"no original time":                   func(rows []map[string]any) { rows[2]["payload"].(map[string]any)["completed_at_ms"] = 0 },
		"foreign target":                     func(rows []map[string]any) { rows[1]["payload"].(map[string]any)["arguments"] = `{"target":"foreign"}` },
		"wrong call":                         func(rows []map[string]any) { rows[3]["payload"].(map[string]any)["call_id"] = "other" },
		"wrong namespace":                    func(rows []map[string]any) { rows[1]["payload"].(map[string]any)["namespace"] = "rewritten" },
		"foreign file":                       func(rows []map[string]any) { rows[0]["payload"].(map[string]any)["id"] = "foreign" },
	} {
		t.Run(name, func(t *testing.T) {
			home, h := lifecycleRollout(t, change)
			if _, err := readSubagentEffects(home, &h); err == nil {
				t.Fatal("unconfirmed effect accepted")
			}
		})
	}
}

func TestSubagentRolloutRejectsUnresolvedLifecycleInTerminalTurn(t *testing.T) {
	home, h := lifecycleRollout(t, func(rows []map[string]any) { rows[2]["type"] = "other" })
	// Another completed Item proves Turn ownership while close has no completion.
	file, err := os.OpenFile(h.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"type":"event_msg","payload":{"type":"item_completed","thread_id":"root","turn_id":"turn","item":{"type":"AgentMessage","id":"answer"}}}` + "\n")
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	h.Turns[0].Items = nil
	if _, err := readSubagentEffects(home, &h); err == nil {
		t.Fatal("lost unknown close effect")
	}
}

func TestSubagentRolloutFiltersInheritedTurnOwnership(t *testing.T) {
	home, h := lifecycleRollout(t, nil)
	h.ID = "child"
	body, err := os.ReadFile(h.Path)
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": "child", "cwd": "/workspace"}})
	// A fork carries ancestor records, including their original thread IDs.
	if err := os.WriteFile(h.Path, append(append(meta, '\n'), body...), 0600); err != nil {
		t.Fatal(err)
	}
	effects, err := readSubagentEffects(home, &h)
	if err != nil || len(effects) != 0 || len(h.Turns) != 0 {
		t.Fatal("inherited parent work became child work", h.Turns, effects, err)
	}
}

func TestSubagentRolloutRejectsEscapingSymlink(t *testing.T) {
	home, h := lifecycleRollout(t, nil)
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.Rename(h.Path, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, h.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := readSubagentEffects(home, &h); err == nil {
		t.Fatal("followed history outside private home")
	}
}
