package mcode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func decodeComponent(value string) (string, error) { return url.PathUnescape(value) }

func (s *Session) handle(frame rpcFrame) error {
	if len(frame.ID) > 0 {
		if frame.Method == "session/request_permission" && s.active {
			return s.askPermission(frame)
		}
		if frame.Method == "elicitation/create" && s.active {
			return s.askQuestion(frame)
		}
		return s.write(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32601, Message: "ACP method not supported by Parsar"}})
	}
	if frame.Method != "session/update" || !s.active {
		return nil
	}
	var event sessionUpdate
	decoder := json.NewDecoder(bytes.NewReader(frame.Params))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		return fmt.Errorf("mcode: invalid session update")
	}
	if event.SessionID != s.sessionID {
		return nil
	}
	s.mu.Lock()
	s.steeringReady = true
	s.mu.Unlock()
	switch event.Update.Kind {
	case "agent_message_chunk":
		if event.Update.Content.Type != "text" {
			return fmt.Errorf("mcode: unsupported response content")
		}
		text := event.Update.Content.Text
		s.content.WriteString(text)
		s.sequence++
		s.emit(proto.TypeDelta, proto.DeltaPayload{Delta: text, Sequence: s.sequence})
	case "agent_thought_chunk":
		s.sequence++
		s.emit(proto.TypeThinking, proto.ThinkingPayload{Text: event.Update.Content.Text, Sequence: s.sequence})
	case "tool_call", "tool_call_update":
		return s.emitTool(event.Update.toolUpdate)
	}
	return nil
}

func (s *Session) emitTool(update toolUpdate) error {
	if update.ID == "" || s.completedTools[update.ID] {
		return nil
	}
	previous, started := s.tools[update.ID]
	if update.Name == "" {
		update.Name = previous.Name
	}
	if update.Name == "" {
		update.Name = update.Title
	}
	if update.RawInput == nil {
		update.RawInput = previous.RawInput
	}
	if s.req.ObserveToolObservations {
		update.mcp = previous.mcp
		if update.mcp != nil && update.Name != previous.Name {
			return fmt.Errorf("mcode: native MCP call identity changed")
		}
		if update.mcp == nil {
			var err error
			update.mcp, err = s.environmentMCPIdentity(update.Name)
			if err != nil {
				return err
			}
		}
		started = previous.mcp != nil || workspaceToolObservation(previous, "before") != nil
	}
	if !started {
		if err := s.emitToolStage(update, "before"); err != nil {
			return err
		}
	}
	s.tools[update.ID] = update
	if update.Status == "completed" || update.Status == "failed" {
		if err := s.emitToolStage(update, "after"); err != nil {
			return err
		}
		delete(s.tools, update.ID)
		s.completedTools[update.ID] = true
	}
	return nil
}

func (s *Session) askPermission(frame rpcFrame) error {
	var request permissionRequest
	if err := json.Unmarshal(frame.Params, &request); err != nil {
		return fmt.Errorf("mcode: invalid permission request")
	}
	if request.SessionID != s.sessionID {
		return fmt.Errorf("mcode: permission request belongs to another session")
	}
	pending := pendingPermission{RPCID: frame.ID}
	for _, option := range request.Options {
		switch option.Kind {
		case "allow_once":
			pending.Allow = option.ID
		case "reject_once":
			pending.Deny = option.ID
		}
	}
	if pending.Allow == "" || pending.Deny == "" {
		return fmt.Errorf("mcode: permission request has no one-time allow/deny options")
	}
	id := "perm_" + uuid.NewString()
	s.mu.Lock()
	s.permissions[id] = pending
	s.mu.Unlock()
	tool := request.ToolCall.Name
	if tool == "" {
		tool = request.ToolCall.Title
	}
	s.emit(proto.TypePermissionRequest, proto.PermissionRequestPayload{RequestID: id, Tool: tool, Title: request.ToolCall.Title, Payload: request.ToolCall.RawInput})
	return nil
}
