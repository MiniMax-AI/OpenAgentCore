package codex

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"sync"
)

type mcpCallIdentity struct{ thread, turn, call string }

type mcpCancellation struct {
	mu       sync.Mutex
	pending  map[mcpCallIdentity]string
	latched  bool
	captured map[string]bool
}

func (s *Session) stdioMCP(label string) bool {
	if s.cfg.view != nil {
		for _, binding := range s.cfg.view.MCP {
			if binding.ServerLabel == label && binding.Transport == "stdio" && binding.Stdio != nil {
				return true
			}
		}
	}
	return false
}

// Native error/end notifications describe the local await, not a remote reply.
// Once cancellation latches, even a later real result cannot release its scope.
func (s *Session) observeMCPCall(thread, turn string, item ThreadItem, started, replied bool) {
	if thread == "" || turn == "" || item.ID == "" || item.Type != "mcpToolCall" || !s.stdioMCP(item.Server) {
		return
	}
	m := &s.mcpCancellation
	m.mu.Lock()
	defer m.mu.Unlock()
	key := mcpCallIdentity{thread, turn, item.ID}
	if !m.latched && replied {
		delete(m.pending, key)
		return
	}
	if started {
		if m.pending == nil {
			m.pending = make(map[mcpCallIdentity]string)
		}
		m.pending[key] = item.Server
		if m.latched {
			m.captured[item.Server] = true
		}
	}
}

func (s *Session) observeRootMCP(raw json.RawMessage, started bool) {
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Item     struct {
			ThreadItem
			Result json.RawMessage `json:"result"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &p) != nil || !s.isRootThread(p.ThreadID) {
		return
	}
	s.steering.mu.Lock()
	matches := p.TurnID != "" && p.TurnID == s.steering.id
	s.steering.mu.Unlock()
	if matches {
		s.observeMCPCall(p.ThreadID, p.TurnID, p.Item.ThreadItem, started, mcpReplied(p.Item.Result))
	}
}

func mcpReplied(result json.RawMessage) bool {
	return len(result) > 0 && string(result) != "null"
}

// Called only for a verified child and a current or interrupted native Turn.
func (s *Session) observeChildMCP(thread, turn string, items []json.RawMessage) {
	for _, raw := range items {
		var item struct {
			ThreadItem
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(raw, &item) == nil {
			replied := mcpReplied(item.Result)
			s.observeMCPCall(thread, turn, item.ThreadItem, !replied, replied)
		}
	}
}

func (s *Session) hasPendingMCP(thread, turn string) bool {
	m := &s.mcpCancellation
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.pending {
		if key.thread == thread && key.turn == turn {
			return true
		}
	}
	return false
}

func (s *Session) latchMCPCancellation() {
	m := &s.mcpCancellation
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latched = true
	m.captured = make(map[string]bool)
	for _, label := range m.pending {
		m.captured[label] = true
	}
}

func (s *Session) cancelledMCPLabels() []string {
	m := &s.mcpCancellation
	m.mu.Lock()
	defer m.mu.Unlock()
	labels := make([]string, 0, len(m.captured))
	for label := range m.captured {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

// Native callbacks and verified child observations have drained before this call.
// The host receipt closes all old alias scopes; the native receipt invalidates
// selected clients throughout the owned thread family without eager discovery.
func (s *Session) cleanupCancelledMCP(ctx context.Context) error {
	labels := s.cancelledMCPLabels()
	if len(labels) == 0 {
		return nil
	}
	if s.cfg.view == nil || s.cfg.view.StopMCP == nil {
		return errors.New("codex: stdio MCP cleanup owner unavailable")
	}
	if err := s.cfg.view.StopMCP(ctx, labels); err != nil {
		return err
	}
	request := func(method string, params any) (json.RawMessage, error) {
		return s.rpc.request(ctx, method, params, func(frame any) error { return s.rpc.writeFrameContext(ctx, frame) })
	}
	raw, err := request("mcpServer/invalidate", McpServerInvalidateParams{ThreadID: s.currentThreadID(), ServerNames: labels})
	if err != nil {
		return err
	}
	var receipt McpServerInvalidateResponse
	if json.Unmarshal(raw, &receipt) != nil || !slices.Equal(receipt.ServerNames, labels) {
		return errors.New("codex: native MCP invalidation unconfirmed")
	}
	_, err = request("config/mcpServer/reload", nil)
	return err
}
