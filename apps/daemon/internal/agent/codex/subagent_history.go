package codex

import (
	"context"
	"encoding/json"
	"errors"
)

type subagentHistory struct {
	Thread
	Parent      string               `json:"parentThreadId"`
	HistoryMode string               `json:"historyMode"`
	Turns       []subagentNativeTurn `json:"turns"`
}

type subagentNativeTurn struct {
	CompletedItems map[string]bool   `json:"-"`
	ID             string            `json:"id"`
	Status         string            `json:"status"`
	StartedAt      *int64            `json:"startedAt"`
	CompletedAt    *int64            `json:"completedAt"`
	ItemsView      string            `json:"itemsView"`
	Items          []json.RawMessage `json:"items"`
}

type subagentCollaboration struct {
	Type      string   `json:"type"`
	ID        string   `json:"id"`
	Tool      string   `json:"tool"`
	Status    string   `json:"status"`
	Sender    string   `json:"senderThreadId"`
	Receivers []string `json:"receiverThreadIds"`
	Prompt    *string  `json:"prompt"`
}

func (s *Session) readSubagentHistory(ctx context.Context, id string) (subagentHistory, error) {
	var result struct {
		Thread subagentHistory `json:"thread"`
	}
	if err := s.subagentRequest(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, &result); err != nil {
		return result.Thread, err
	}
	h := result.Thread
	if h.ID != id || h.HistoryMode != "paginated" || h.Path == "" {
		return h, errors.New("codex: subagent history identity or mode unavailable")
	}
	var cursor *string
	seen := map[string]bool{}
	for range 100 {
		var page struct {
			Data       []subagentNativeTurn `json:"data"`
			NextCursor *string              `json:"nextCursor"`
		}
		args := map[string]any{"threadId": id, "itemsView": "full", "sortDirection": "asc", "limit": 100, "cursor": cursor}
		if err := s.subagentRequest(ctx, "thread/turns/list", args, &page); err != nil {
			return h, err
		}
		if page.Data == nil {
			return h, errors.New("codex: subagent Turn history unavailable")
		}
		for _, turn := range page.Data {
			if turn.ID == "" || seen[turn.ID] || turn.ItemsView != "full" {
				return h, errors.New("codex: subagent Turn history incomplete")
			}
			seen[turn.ID] = true
			h.Turns = append(h.Turns, turn)
		}
		if page.NextCursor == nil {
			return h, nil
		}
		cursor = page.NextCursor
	}
	return h, errors.New("codex: subagent Turn history exceeds read bound")
}

func (s *Session) subagentRequest(ctx context.Context, method string, args, result any) error {
	operation, cancel := context.WithTimeout(ctx, subagentLookupTimeout)
	defer cancel()
	raw, err := s.rpc.requestWithTimeout(operation, method, args, func(frame any) error { return s.rpc.writeFrameContext(operation, frame) }, 0, nil)
	if err != nil {
		return errors.New("codex: subagent native history query failed")
	}
	if json.Unmarshal(raw, result) != nil {
		return errors.New("codex: invalid subagent native history")
	}
	return nil
}
