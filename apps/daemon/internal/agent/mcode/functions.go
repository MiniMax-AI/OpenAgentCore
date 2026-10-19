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
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

// functionServer serves the Session's public functions as native MCP tools. It
// listens on loopback only and requires a per-Session bearer token, so only the
// native process that received the ACP server entry can call it.
type functionServer struct {
	token string
	url   string
	http  *http.Server
	done  chan struct{}
	owner atomic.Pointer[executor]
}

func serveFunctions(tools []proto.FunctionTool) (*functionServer, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	f := &functionServer{token: rand.Text(), done: make(chan struct{})}
	server, err := f.newServer(tools)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.url = "http://" + listener.Addr().String() + "/mcp"
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

// newServer registers the tools before any listener exists. AddTool panics on a
// schema it rejects, which becomes an error here.
func (f *functionServer) newServer(tools []proto.FunctionTool) (server *mcp.Server, err error) {
	defer func() {
		if recover() != nil {
			server, err = nil, errors.New("mcode: the MCP server rejected a function schema")
		}
	}()
	server = mcp.NewServer(&mcp.Implementation{Name: declaration.FunctionServerLabel, Version: "1"}, nil)
	for _, tool := range tools {
		server.AddTool(&mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters}, f.call)
	}
	return server, nil
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
	_ = f.http.Close()
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
	call proto.FunctionCallPayload
	// settled closes when the result is delivered or the call is released; a nil result means released.
	settled chan struct{}
	receipt chan error
	result  *proto.FunctionResultPayload
	content []mcp.Content
}

// functionCalls is one Turn's pending calls. emit holds the lock so a stopped
// Turn never writes to its closed output.
type functionCalls struct {
	mu      sync.Mutex
	pending map[string]*pendingFunction
	stopped chan struct{}
	closed  bool
}

// functionCallMeta is the CallToolResult _meta key that native echoes in its tool update.
const functionCallMeta = "oac_call_id"

func (s *Session) callFunction(ctx context.Context, name string, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	id := "call_" + rand.Text()
	p := &pendingFunction{call: proto.FunctionCallPayload{CallID: id, Name: name, Arguments: arguments}, settled: make(chan struct{}), receipt: make(chan error, 1)}
	s.functions.mu.Lock()
	if s.functions.closed {
		s.functions.mu.Unlock()
		return nil, agent.ErrUnknownFunctionCall
	}
	s.functions.pending[id] = p
	s.emit(proto.TypeToolCall, proto.ToolCallPayload{ID: id, Name: name, Stage: "before", Observation: &proto.ToolObservation{Kind: "function", Status: "in_progress", Name: name, Arguments: arguments}})
	s.emit(proto.TypeFunctionCall, p.call)
	s.functions.mu.Unlock()
	select {
	case <-p.settled:
		if p.result == nil {
			return nil, agent.ErrUnknownFunctionCall
		}
		return &mcp.CallToolResult{Content: p.content, IsError: !p.result.Success, Meta: mcp.Meta{functionCallMeta: id}}, nil
	case <-s.functions.stopped:
		return nil, agent.ErrUnknownFunctionCall
	case <-ctx.Done():
		s.functions.mu.Lock()
		s.release(p)
		s.functions.mu.Unlock()
		return nil, ctx.Err()
	}
}

// release drops a call that native ended without confirming its result,
// withdraws it from a live Turn and unblocks its waiter. The caller holds
// s.functions.mu.
func (s *Session) release(p *pendingFunction) {
	if s.functions.pending[p.call.CallID] != p {
		return
	}
	delete(s.functions.pending, p.call.CallID)
	if !s.functions.closed {
		s.emit(proto.TypeFunctionCallWithdrawn, proto.FunctionCallWithdrawnPayload{CallID: p.call.CallID})
	}
	if p.result == nil {
		close(p.settled)
	}
	p.receipt <- agent.ErrUnknownFunctionCall
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
	close(p.settled)
	s.functions.mu.Unlock()
	// The receive loop calls this inline, so the wait is bounded.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case err := <-p.receipt:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// confirmFunction applies a terminal native update for a function tool. Native
// echoes the returned result, with the call ID in its _meta, only when it
// received one; any other end means native gave up on the call, such as its
// 120 s timeout.
// Native runs MCP tools one at a time, so at most one call is in flight.
func (s *Session) confirmFunction(update toolUpdate) {
	s.functions.mu.Lock()
	defer s.functions.mu.Unlock()
	var echo struct {
		Details struct {
			MCP struct {
				Meta map[string]any `json:"_meta"`
			} `json:"mcp"`
		} `json:"details"`
	}
	raw, _ := json.Marshal(update.RawOutput)
	_ = json.Unmarshal(raw, &echo)
	id, _ := echo.Details.MCP.Meta[functionCallMeta].(string)
	p := s.functions.pending[id]
	if p == nil {
		for _, q := range s.functions.pending {
			if q.call.Name == update.mcp.tool {
				p = q
			}
		}
	}
	if p == nil {
		return
	}
	if id != p.call.CallID || p.result == nil {
		s.release(p)
		return
	}
	delete(s.functions.pending, id)
	status := "completed"
	if update.Status != "completed" || !p.result.Success {
		status = "failed"
	}
	s.emit(proto.TypeToolCall, proto.ToolCallPayload{ID: id, Name: p.call.Name, Stage: "after", Observation: &proto.ToolObservation{Kind: "function", Status: status, Name: p.call.Name, Arguments: p.call.Arguments, Content: &p.result.Content}})
	p.receipt <- nil
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
	close(s.functions.stopped)
	for _, p := range s.functions.pending {
		s.release(p)
	}
}
