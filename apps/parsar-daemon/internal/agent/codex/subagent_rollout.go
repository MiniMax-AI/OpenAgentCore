package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type subagentEffect struct {
	proto.SubagentLifecyclePayload
	source, turn, call string
}
type rolloutCall struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
}
type rolloutCompletion struct {
	Thread      string `json:"thread_id"`
	Turn        string `json:"turn_id"`
	CompletedAt int64  `json:"completed_at_ms"`
	Item        struct {
		Type      string   `json:"type"`
		ID        string   `json:"id"`
		Tool      string   `json:"tool"`
		Status    string   `json:"status"`
		Sender    string   `json:"sender_thread_id"`
		Receivers []string `json:"receiver_thread_ids"`
	} `json:"item"`
}

// The outer rollout timestamp is a write time. Only correlated native
// ItemCompleted.completed_at_ms may timestamp a successful lifecycle effect.
func readSubagentEffects(home string, h *subagentHistory) ([]subagentEffect, error) {
	rel, err := filepath.Rel(home, h.Path)
	if !filepath.IsAbs(home) || !filepath.IsAbs(h.Path) || err != nil || !strings.HasPrefix(rel, "sessions"+string(filepath.Separator)) {
		return nil, errors.New("codex: subagent history escaped private native home")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, errors.New("codex: private subagent history unavailable")
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return nil, errors.New("codex: subagent rollout unavailable")
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 64<<20 {
		return nil, errors.New("codex: subagent rollout exceeds read bound")
	}
	body, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	if err != nil || len(body) > 64<<20 {
		return nil, errors.New("codex: subagent rollout read failed")
	}
	if len(body) == 0 || body[len(body)-1] != '\n' {
		return nil, errors.New("codex: subagent rollout not flushed")
	}
	calls := map[string]rolloutCall{}
	outputs := map[string]json.RawMessage{}
	done := map[string]rolloutCompletion{}
	verified := false
	owners := map[string]string{}
	ownedItems := map[string]map[string]bool{}
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var row struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(line, &row) != nil {
			return nil, errors.New("codex: invalid subagent rollout")
		}
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(row.Payload, &kind) != nil {
			return nil, errors.New("codex: invalid subagent rollout payload")
		}
		switch row.Type {
		case "session_meta":
			if verified {
				continue
			}
			var meta struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(row.Payload, &meta) != nil || meta.ID != h.ID || meta.Cwd != h.Cwd {
				return nil, errors.New("codex: subagent rollout ownership mismatch")
			}
			verified = true
		case "response_item":
			if kind.Type == "function_call" {
				var call rolloutCall
				if json.Unmarshal(row.Payload, &call) != nil {
					return nil, errors.New("codex: invalid lifecycle call")
				}
				if call.Name != "close_agent" && call.Name != "resume_agent" {
					continue
				}
				if call.Namespace != "multi_agent_v1" || call.CallID == "" {
					return nil, errors.New("codex: lifecycle call is not a direct native tool")
				}
				if _, ok := calls[call.CallID]; ok {
					return nil, errors.New("codex: duplicate lifecycle call")
				}
				calls[call.CallID] = call
			} else if kind.Type == "function_call_output" {
				var output struct {
					CallID string          `json:"call_id"`
					Output json.RawMessage `json:"output"`
				}
				if json.Unmarshal(row.Payload, &output) != nil {
					return nil, errors.New("codex: invalid lifecycle output")
				}
				if prior, ok := outputs[output.CallID]; ok && !bytes.Equal(prior, output.Output) {
					return nil, errors.New("codex: conflicting lifecycle output")
				}
				outputs[output.CallID] = output.Output
			}
		case "event_msg":
			if kind.Type != "item_completed" {
				continue
			}
			var completed rolloutCompletion
			if json.Unmarshal(row.Payload, &completed) != nil {
				return nil, errors.New("codex: invalid lifecycle completion")
			}
			if completed.Thread == "" || completed.Turn == "" {
				return nil, errors.New("codex: native Item owner unavailable")
			}
			if prior, ok := owners[completed.Turn]; ok && prior != completed.Thread {
				return nil, errors.New("codex: conflicting native Turn owner")
			}
			owners[completed.Turn] = completed.Thread
			if ownedItems[completed.Turn] == nil {
				ownedItems[completed.Turn] = map[string]bool{}
			}
			ownedItems[completed.Turn][completed.Item.ID] = true
			if completed.Item.Type != "CollabAgentToolCall" || (completed.Item.Tool != "close_agent" && completed.Item.Tool != "resume_agent") {
				continue
			}
			if _, ok := done[completed.Item.ID]; ok {
				return nil, errors.New("codex: duplicate lifecycle completion")
			}
			done[completed.Item.ID] = completed
		}
	}
	if !verified {
		return nil, errors.New("codex: missing subagent rollout identity")
	}
	owned := make([]subagentNativeTurn, 0, len(h.Turns))
	for _, turn := range h.Turns {
		owner, ok := owners[turn.ID]
		if !ok {
			if turn.Status == "inProgress" {
				continue
			}
			return nil, errors.New("codex: native child Turn owner not persisted")
		}
		if owner == h.ID {
			turn.CompletedItems = ownedItems[turn.ID]
			owned = append(owned, turn)
		}
	}
	h.Turns = owned
	active := false
	covered := map[string]bool{}
	for _, turn := range h.Turns {
		if turn.Status == "inProgress" {
			active = true
		}
	}
	var effects []subagentEffect
	for _, turn := range h.Turns {
		for _, raw := range turn.Items {
			var item subagentCollaboration
			if json.Unmarshal(raw, &item) != nil {
				return nil, errors.New("codex: invalid native history item")
			}
			if item.Type != "collabAgentToolCall" || (item.Tool != "closeAgent" && item.Tool != "resumeAgent") {
				continue
			}
			covered[item.ID] = true
			if item.Status == "inProgress" {
				if turn.Status != "inProgress" {
					return nil, errors.New("codex: lifecycle effect remains unconfirmed in a terminal Turn")
				}
				continue
			}
			completion, ok := done[item.ID]
			call, hasCall := calls[item.ID]
			output, hasOutput := outputs[item.ID]
			if !ok || !hasCall || !hasOutput {
				return nil, errors.New("codex: lifecycle outcome is not durably confirmed")
			}
			if completion.Thread != h.ID || completion.Turn != turn.ID || completion.Item.Sender != h.ID || completion.Item.ID != item.ID || completion.Item.Tool != call.Name || completion.CompletedAt <= 0 || completion.Item.Status != item.Status {
				return nil, errors.New("codex: lifecycle correlation mismatch")
			}
			if item.Status == "failed" {
				continue
			}
			if item.Status != "completed" || len(item.Receivers) != 1 || len(completion.Item.Receivers) != 1 || completion.Item.Receivers[0] != item.Receivers[0] {
				return nil, errors.New("codex: lifecycle target is unverified")
			}
			var args map[string]json.RawMessage
			var target string
			field, key, status := "target", "previous_status", "closed"
			if call.Name == "resume_agent" {
				field, key, status = "id", "status", "active"
			}
			if json.Unmarshal([]byte(call.Arguments), &args) != nil || json.Unmarshal(args[field], &target) != nil || target != item.Receivers[0] {
				return nil, errors.New("codex: lifecycle target mismatch")
			}
			var text string
			var receipt map[string]json.RawMessage
			if json.Unmarshal(output, &text) != nil || json.Unmarshal([]byte(text), &receipt) != nil || len(receipt) != 1 || !validSubagentStatus(receipt[key]) {
				return nil, errors.New("codex: lifecycle success receipt unavailable")
			}
			effects = append(effects, subagentEffect{proto.SubagentLifecyclePayload{NativeID: target, EffectID: h.ID + ":" + item.ID, Status: status, OccurredAtMS: completion.CompletedAt}, h.ID, turn.ID, item.ID})
		}
	}
	for call := range calls {
		if _, ok := done[call]; !ok && !active {
			return nil, errors.New("codex: lifecycle call has no confirmed terminal outcome")
		}
	}
	for id, event := range done {
		if event.Thread == h.ID && !covered[id] {
			return nil, errors.New("codex: lifecycle history snapshot is incomplete")
		}
	}
	return effects, nil
}

func validSubagentStatus(raw json.RawMessage) bool {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		switch text {
		case "pending_init", "running", "interrupted", "shutdown", "not_found":
			return true
		}
		return false
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || len(value) != 1 {
		return false
	}
	if body, ok := value["completed"]; ok {
		return bytes.Equal(body, []byte("null")) || json.Unmarshal(body, &text) == nil
	}
	if body, ok := value["errored"]; ok {
		return json.Unmarshal(body, &text) == nil
	}
	return false
}
