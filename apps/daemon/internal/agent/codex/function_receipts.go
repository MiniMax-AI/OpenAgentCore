package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type functionReply struct {
	Success      bool              `json:"success"`
	ContentItems []functionContent `json:"contentItems"`
}

type pendingFunction struct {
	rpcID   any
	turnID  string
	name    string
	reply   *functionReply
	receipt chan error
}

func (s *Session) SubmitFunctionResult(ctx context.Context, result proto.FunctionResultPayload) error {
	if !s.beginOperation() {
		return agent.ErrSteeringInactive
	}
	defer s.endOperation()
	if s.functions == nil {
		return agent.ErrUnknownFunctionCall
	}
	if err := result.ValidateContent(); err != nil {
		return err
	}
	// Own the submitted content while the caller and native reader run independently.
	reply := functionReply{Success: result.Success, ContentItems: make([]functionContent, 0, len(result.Content))}
	for _, part := range result.Content {
		value := functionContent{Type: "inputText"}
		if part.Text != nil {
			text := *part.Text
			value.Text = &text
		} else {
			image := *part.ImageURL
			value.Type, value.ImageURL = "inputImage", &image
		}
		reply.ContentItems = append(reply.ContentItems, value)
	}
	s.functions.mu.Lock()
	pending := s.functions.pending[result.CallID]
	if pending == nil || pending.reply != nil || s.functions.closed || s.cancelled.Load() || s.terminal.Load() {
		s.functions.mu.Unlock()
		return agent.ErrUnknownFunctionCall
	}
	pending.reply = &reply
	s.functions.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// This operation owns both transport failure and native application. A generic
	// write deadline must not close the process after a native receipt has won.
	written := make(chan error, 1)
	if !s.beginOperation() {
		return agent.ErrUnknownFunctionCall
	}
	go func() {
		defer s.endOperation()
		if err := ctx.Err(); err != nil {
			written <- err
			return
		}
		written <- s.rpc.writeFrame(JsonRpcResponse{JsonRpc: JsonRpcVersion, ID: pending.rpcID, Result: reply})
	}()
	return s.waitFunctionResult(ctx, result.CallID, pending, written)
}

func (s *Session) waitFunctionResult(ctx context.Context, callID string, pending *pendingFunction, written <-chan error) error {
	for {
		var failure error
		select {
		case err := <-pending.receipt:
			return err
		case err := <-written:
			written = nil
			if err == nil {
				continue
			}
			failure = fmt.Errorf("write function result: %w", err)
		case <-s.rpc.Done():
			failure = agent.ErrUnknownFunctionCall
		case <-s.cancelCtx.Done():
			failure = agent.ErrUnknownFunctionCall
		case <-ctx.Done():
			failure = ctx.Err()
		}
		// Only the unconfirmed-outcome owner may terminate native execution. Do not
		// join a confirmed writer: native consumption already proves the full frame,
		// and its final buffered completion cannot block submission or Run release.
		if s.settleFunctionResult(callID, failure) {
			s.cancelFn()
			_ = s.rpc.Close()
		}
		return <-pending.receipt
	}
}

// The map owns settlement. Removing a call and writing its buffered receipt are
// atomic with shutdown; no waiter, native IO or output send holds this lock.
func (s *Session) settleFunctionResult(callID string, err error) bool {
	s.functions.mu.Lock()
	defer s.functions.mu.Unlock()
	if pending := s.functions.pending[callID]; pending != nil {
		delete(s.functions.pending, callID)
		pending.receipt <- err
		return true
	}
	return false
}

func (s *Session) confirmFunctionResult(raw json.RawMessage) {
	if s.functions == nil {
		return
	}
	var event struct {
		ThreadID string                `json:"threadId"`
		TurnID   string                `json:"turnId"`
		Item     toolObservationSource `json:"item"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Item.Type != "dynamicToolCall" || !s.isRootTurn(event.ThreadID, event.TurnID) {
		return
	}
	s.functions.mu.Lock()
	defer s.functions.mu.Unlock()
	pending := s.functions.pending[event.Item.ID]
	if pending == nil || pending.reply == nil || s.functions.closed || s.cancelled.Load() {
		return
	}
	item := event.Item
	status := "completed"
	if !pending.reply.Success {
		status = "failed"
	}
	var err error
	if pending.turnID != event.TurnID || pending.name != item.Tool || item.Namespace != "" || item.Status != status || item.Success == nil || *item.Success != pending.reply.Success || item.ContentItems == nil || !reflect.DeepEqual(*item.ContentItems, pending.reply.ContentItems) {
		err = errors.New("codex: native function receipt does not match submitted result")
	}
	delete(s.functions.pending, item.ID)
	pending.receipt <- err
}
