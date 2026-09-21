package codex

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) snapshotSubagents(ctx context.Context) (bool, error) {
	root := s.currentThreadID()
	queue := []string{root}
	known := map[string]bool{root: true}
	parents := map[string]string{}
	opened := map[string]int64{}
	var histories []subagentHistory
	var effects []subagentEffect
	busy := false
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		h, err := s.readSubagentHistory(ctx, id)
		if err != nil {
			return false, err
		}
		if id != root && h.Parent != parents[id] {
			return false, errors.New("codex: child history parent mismatch")
		}
		if id != root {
			h.CreatedAt = opened[id]
		}
		observed, err := readSubagentEffects(s.nativeHome, &h)
		if err != nil {
			return false, err
		}
		effects = append(effects, observed...)
		histories = append(histories, h)
		for _, turn := range h.Turns {
			for _, raw := range turn.Items {
				var item subagentCollaboration
				if json.Unmarshal(raw, &item) != nil {
					return false, errors.New("codex: invalid subagent discovery item")
				}
				if item.Type != "collabAgentToolCall" || item.Tool != "spawnAgent" || item.Status != "completed" {
					continue
				}
				if item.Sender != id || item.ID == "" || len(item.Receivers) != 1 {
					return false, errors.New("codex: subagent spawn identity is unverified")
				}
				child := item.Receivers[0]
				if child == "" || child == root {
					return false, errors.New("codex: invalid subagent tree")
				}
				if known[child] {
					if parents[child] != id {
						return false, errors.New("codex: conflicting child ancestry")
					}
					continue
				}
				if len(known) >= 65 {
					return false, errors.New("codex: subagent tree exceeds read bound")
				}
				budget := 64
				metadata, found, err := s.persistedSubagent(ctx, child, id, &budget)
				if err != nil {
					return false, err
				}
				if !found {
					return false, errors.New("codex: subagent identity not yet persisted")
				}
				identity := proto.SubagentIdentityPayload{NativeID: child, ParentNativeID: id, NativeCreatedAt: metadata.CreatedAt, ParentTurnID: turn.ID, SourceItemID: item.ID, Name: metadata.Nickname, Instructions: item.Prompt}
				if err = s.sendSubagentFact(ctx, proto.TypeSubagentIdentity, child, identity); err != nil {
					return false, err
				}
				known[child] = true
				parents[child] = id
				opened[child] = metadata.CreatedAt
				queue = append(queue, child)
			}
		}
	}
	// Reconstruct transitions from the complete tree each time. A successful native
	// resume of an already-active identity does not reopen it in the public model.
	sort.SliceStable(effects, func(i, j int) bool {
		if effects[i].OccurredAtMS == effects[j].OccurredAtMS {
			return effects[i].EffectID < effects[j].EffectID
		}
		return effects[i].OccurredAtMS < effects[j].OccurredAtMS
	})
	closed := map[string]bool{}
	lastEffect := map[string]subagentEffect{}
	for _, effect := range effects {
		if !known[effect.NativeID] || effect.NativeID == root {
			return false, errors.New("codex: lifecycle effect targets an unverified child")
		}
		if previous, ok := lastEffect[effect.NativeID]; ok && previous.OccurredAtMS == effect.OccurredAtMS && previous.Status != effect.Status {
			return false, errors.New("codex: lifecycle effect order is ambiguous")
		}
		lastEffect[effect.NativeID] = effect
		if effect.Status == "active" && !closed[effect.NativeID] {
			continue
		}
		closed[effect.NativeID] = effect.Status == "closed"
		if err := s.sendSubagentFact(ctx, proto.TypeSubagentLifecycle, effect.EffectID, effect.SubagentLifecyclePayload); err != nil {
			return false, err
		}
	}
	for _, h := range histories {
		if h.ID == root {
			if err := s.publishSubagentCoordination(ctx, h, known); err != nil {
				return false, err
			}
		}
		if h.ID != root {
			active, err := s.publishSubagentHistory(ctx, h, known)
			if err != nil {
				return false, err
			}
			busy = busy || active
		}
	}
	return busy, nil
}

func (s *Session) publishSubagentHistory(ctx context.Context, h subagentHistory, known map[string]bool) (bool, error) {
	busy := false
	var nativeStatus struct {
		Type string `json:"type"`
	}
	rawStatus, _ := json.Marshal(h.Status)
	if json.Unmarshal(rawStatus, &nativeStatus) != nil {
		return false, errors.New("codex: native child execution status unavailable")
	}
	for _, turn := range h.Turns {
		status := turn.Status
		switch status {
		case "inProgress":
			if nativeStatus.Type == "notLoaded" {
				return false, errors.New("codex: unfinished child work has no live native owner")
			}
			status = "in_progress"
			busy = true
			if s.subagents.cancelling.Load() {
				key := h.ID + ":" + turn.ID
				if !s.subagents.interrupted[key] {
					var ack json.RawMessage
					if err := s.subagentRequest(ctx, "turn/interrupt", TurnInterruptParams{ThreadID: h.ID, TurnID: turn.ID}, &ack); err != nil {
						return false, err
					}
					s.subagents.interrupted[key] = true
				}
			}
		case "completed", "failed":
		case "interrupted":
			status = "cancelled"
		default:
			return false, errors.New("codex: unknown child Turn status")
		}
		created := h.CreatedAt * 1000
		started, completed := nativeMilliseconds(turn.StartedAt), nativeMilliseconds(turn.CompletedAt)
		if started != nil {
			created = *started
		} else if completed != nil {
			created = *completed
		}
		if created <= 0 {
			return false, errors.New("codex: child Turn has no native timestamp")
		}
		payload := proto.SubagentTurnPayload{NativeID: h.ID, TurnID: turn.ID, Status: "in_progress", CreatedAtMS: created, StartedAtMS: started}
		key := h.ID + ":" + turn.ID
		// A replay never regresses a terminal Turn to active inside this Run.
		if _, sent := s.subagents.sent[proto.TypeSubagentTurn+":"+key]; !sent {
			if err := s.sendSubagentFact(ctx, proto.TypeSubagentTurn, key, payload); err != nil {
				return false, err
			}
		}
		for position, raw := range turn.Items {
			itemStatus := status
			var identity struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &identity)
			if turn.CompletedItems[identity.ID] {
				itemStatus = "completed"
			}
			kind, body, id, err := subagentItemSnapshot(raw, itemStatus)
			if kind == proto.TypeSubagentCoordination {
				value, decodeErr := subagentCoordination(raw, h.ID)
				if decodeErr != nil {
					return false, decodeErr
				}
				for _, target := range value.Recipients {
					if !known[target] && value.Status != "failed" {
						return false, errors.New("codex: coordination recipient has no verified identity")
					}
				}
				body, err = json.Marshal(value)
			}
			if err != nil {
				return false, err
			}
			if kind == "" {
				continue
			}
			item := proto.SubagentItemPayload{NativeID: h.ID, TurnID: turn.ID, ItemID: id, Position: int32(position), Kind: kind, Payload: body}
			if err := s.sendSubagentFact(ctx, proto.TypeSubagentItem, key+":"+id, item); err != nil {
				return false, err
			}
		}
		if status != "in_progress" {
			if completed == nil {
				return false, errors.New("codex: child completion timestamp not persisted")
			}
			payload.Status = status
			payload.CompletedAtMS = completed
			if err := s.sendSubagentFact(ctx, proto.TypeSubagentTurn, key, payload); err != nil {
				return false, err
			}
		}
	}
	return busy || nativeStatus.Type == "active", nil
}

func nativeMilliseconds(seconds *int64) *int64 {
	if seconds == nil {
		return nil
	}
	ms := *seconds * 1000
	return &ms
}

func subagentItemSnapshot(raw json.RawMessage, turnStatus string) (string, json.RawMessage, string, error) {
	var item struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Text   string `json:"text"`
		Phase  string `json:"phase"`
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return "", nil, "", errors.New("codex: invalid child Item")
	}
	if item.ID == "" {
		return "", nil, "", errors.New("codex: child Item has no identity")
	}
	switch item.Type {
	case "agentMessage":
		status := "in_progress"
		if turnStatus == "completed" {
			status = "completed"
		} else if turnStatus != "in_progress" {
			status = "incomplete"
		}
		body, err := json.Marshal(proto.OutputMessagePayload{ID: item.ID, Status: status, Text: &item.Text, Phase: item.Phase})
		return proto.TypeOutputMessage, body, item.ID, err
	case "commandExecution", "fileChange", "mcpToolCall", "dynamicToolCall", "webSearch":
		stage := "after"
		if item.Status == "inProgress" {
			stage = "before"
		}
		observation, err := normalizeToolObservation(item.ID, stage, raw)
		if err != nil {
			return "", nil, "", err
		}
		body, err := json.Marshal(proto.ToolCallPayload{ID: item.ID, Stage: stage, Observation: observation})
		return proto.TypeToolCall, body, item.ID, err
	case "collabAgentToolCall":
		return proto.TypeSubagentCoordination, nil, item.ID, nil
	case "userMessage":
		var source struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &source) != nil {
			return "", nil, "", errors.New("codex: invalid child input")
		}
		content := make([]map[string]string, 0, len(source.Content))
		for _, part := range source.Content {
			if part.Type != "text" {
				return "", nil, "", errors.New("codex: unsupported child input content")
			}
			content = append(content, map[string]string{"type": "input_text", "text": part.Text})
		}
		body, err := json.Marshal(map[string]any{"input": []any{map[string]any{"type": "message", "role": "user", "content": content}}})
		return "message", body, item.ID, err
	case "reasoning":
		var source struct {
			Summary []string `json:"summary"`
		}
		if json.Unmarshal(raw, &source) != nil {
			return "", nil, "", errors.New("codex: invalid child reasoning")
		}
		summary := make([]map[string]string, 0, len(source.Summary))
		for _, text := range source.Summary {
			summary = append(summary, map[string]string{"type": "summary_text", "text": text})
		}
		status := "in_progress"
		if turnStatus == "completed" {
			status = "completed"
		} else if turnStatus != "in_progress" {
			status = "incomplete"
		}
		body, err := json.Marshal(map[string]any{"status": status, "summary": summary})
		return "reasoning", body, item.ID, err
	default:
		return "", nil, "", errors.New("codex: unsupported child Item observation")
	}
}
