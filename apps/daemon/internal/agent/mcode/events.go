package mcode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func decodeComponent(value string) (string, error) { return url.PathUnescape(value) }

func (s *Session) handle(frame rpcFrame) error {
	if len(frame.ID) > 0 {
		// Harnesses run unattended, so no client method other than the
		// permission answer is offered. Pinned mcode 0.4.12 maps the ACP
		// cancelled outcome to deny: the tool call is blocked and the Turn
		// continues.
		if frame.Method == "session/request_permission" {
			return s.write(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Result: json.RawMessage(`{"outcome":{"outcome":"cancelled"}}`)})
		}
		return s.write(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32601, Message: "ACP method not supported by OpenAgentCore"}})
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
	previous := s.tools[update.ID]
	if update.Name == "" {
		update.Name = previous.Name
	}
	if update.Name == "" {
		update.Name = update.Title
	}
	if update.RawInput == nil {
		update.RawInput = previous.RawInput
	}
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
	if previous.mcp == nil && workspaceToolObservation(previous, "before") == nil {
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
