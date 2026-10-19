package mcode

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

// functionServer serves the Session's public functions as native MCP tools. It
// listens on loopback only and requires a per-Session bearer token, so only the
// native process that received the ACP server entry can call it.
type functionServer struct {
	token    string
	url      string
	http     *http.Server
	done     chan struct{}
	owner    atomic.Pointer[executor]
	stopOnce sync.Once
}

func serveFunctions(tools []proto.FunctionTool) (*functionServer, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f := &functionServer{token: rand.Text(), url: "http://" + listener.Addr().String() + "/mcp", done: make(chan struct{})}
	server := mcp.NewServer(&mcp.Implementation{Name: declaration.FunctionServerLabel, Version: "1"}, nil)
	for _, tool := range tools {
		server.AddTool(&mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters}, f.call)
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	want := []byte("Bearer " + f.token)
	f.http = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})}
	go func() {
		defer close(f.done)
		_ = f.http.Serve(listener)
	}()
	return f, nil
}

// mcpServer is the ACP mcpServers entry; the token stays in transient ACP headers.
func (f *functionServer) mcpServer() map[string]any {
	return map[string]any{"name": declaration.FunctionServerLabel, "type": "http", "url": f.url,
		"headers": []map[string]string{{"name": "Authorization", "value": "Bearer " + f.token}}}
}

// close stops the listener and every connection, then waits for the serving goroutine.
func (f *functionServer) close() {
	if f == nil {
		return
	}
	f.stopOnce.Do(func() { _ = f.http.Close() })
	<-f.done
}

// call runs on the HTTP handler goroutine; the active Turn owns the call.
func (f *functionServer) call(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	e := f.owner.Load()
	if e == nil {
		return nil, agent.ErrUnknownFunctionCall
	}
	e.mu.Lock()
	s := e.active
	e.mu.Unlock()
	if s == nil {
		return nil, agent.ErrUnknownFunctionCall
	}
	return s.callFunction(ctx, req.Params.Name, req.Params.Arguments)
}

type pendingFunction struct {
	call      proto.FunctionCallPayload
	delivered chan struct{}
	stopped   chan struct{}
	receipt   chan error
	result    *proto.FunctionResultPayload
	content   []mcp.Content
	seq       int
}

// functionCalls is one Turn's pending calls. emit holds the lock so a stopped
// Turn never writes to its closed output.
type functionCalls struct {
	mu      sync.Mutex
	pending map[string]*pendingFunction
	next    int
	closed  bool
}

func (s *Session) callFunction(ctx context.Context, name string, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage("{}")
	}
	id := "call_" + rand.Text()
	p := &pendingFunction{call: proto.FunctionCallPayload{CallID: id, Name: name, Arguments: arguments}, delivered: make(chan struct{}), stopped: make(chan struct{}), receipt: make(chan error, 1)}
	s.functions.mu.Lock()
	if s.functions.closed {
		s.functions.mu.Unlock()
		return nil, agent.ErrUnknownFunctionCall
	}
	s.functions.next++
	p.seq = s.functions.next
	s.functions.pending[id] = p
	s.emit(proto.TypeToolCall, proto.ToolCallPayload{ID: id, Name: name, Stage: "before", Observation: &proto.ToolObservation{Kind: "function", Status: "in_progress", Name: name, Arguments: arguments}})
	s.emit(proto.TypeFunctionCall, p.call)
	s.functions.mu.Unlock()
	select {
	case <-p.delivered:
		return &mcp.CallToolResult{Content: p.content, IsError: !p.result.Success}, nil
	case <-p.stopped:
		return nil, agent.ErrUnknownFunctionCall
	case <-ctx.Done():
		s.functions.mu.Lock()
		delete(s.functions.pending, id)
		p.receipt <- agent.ErrUnknownFunctionCall
		s.functions.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *Session) SubmitFunctionResult(ctx context.Context, result proto.FunctionResultPayload) error {
	if err := result.ValidateContent(); err != nil {
		return err
	}
	content := make([]mcp.Content, 0, len(result.Content))
	for _, part := range result.Content {
		if part.Text == nil {
			return errors.New("mcode: function result images are not supported")
		}
		content = append(content, &mcp.TextContent{Text: *part.Text})
	}
	s.functions.mu.Lock()
	p := s.functions.pending[result.CallID]
	if s.functions.closed || p == nil || p.result != nil {
		s.functions.mu.Unlock()
		return agent.ErrUnknownFunctionCall
	}
	p.result, p.content = &result, content
	close(p.delivered)
	s.functions.mu.Unlock()
	select {
	case err := <-p.receipt:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// confirmFunction applies the native result: the oldest delivered, unconfirmed
// call to this function is the one that native reports.
func (s *Session) confirmFunction(update toolUpdate) {
	s.functions.mu.Lock()
	defer s.functions.mu.Unlock()
	var match *pendingFunction
	for _, p := range s.functions.pending {
		if p.result != nil && p.call.Name == update.mcp.tool && (match == nil || p.seq < match.seq) {
			match = p
		}
	}
	if match == nil {
		return
	}
	delete(s.functions.pending, match.call.CallID)
	status := "completed"
	if !match.result.Success {
		status = "failed"
	}
	s.emit(proto.TypeToolCall, proto.ToolCallPayload{ID: match.call.CallID, Name: match.call.Name, Stage: "after", Observation: &proto.ToolObservation{Kind: "function", Status: status, Name: match.call.Name, Arguments: match.call.Arguments, Content: &match.result.Content}})
	match.receipt <- nil
}

// stopFunctions ends the Turn's calls: native gets an error for each pending
// call and each unconfirmed submission fails.
func (s *Session) stopFunctions() {
	s.functions.mu.Lock()
	defer s.functions.mu.Unlock()
	if s.functions.closed {
		return
	}
	s.functions.closed = true
	for id, p := range s.functions.pending {
		close(p.stopped)
		p.receipt <- agent.ErrUnknownFunctionCall
		delete(s.functions.pending, id)
	}
}
