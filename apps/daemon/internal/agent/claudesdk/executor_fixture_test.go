//go:build unix

package claudesdk

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// testBridge runs a bridge fixture as a view Executor whose view paths are
// host paths.
type testBridge struct {
	// Config is the probe. Its Env also reaches the bridge.
	Config
	// Home is the Session home, at the same path in the view.
	Home string
	// Workspace is the workspace root of a local Environment request.
	Workspace string
}

// StateDir is the bridge's CLAUDE_CONFIG_DIR.
func (b testBridge) StateDir() string { return filepath.Join(b.Home, "config") }

// factory is the view Executor factory over b, as the agent host runs it for
// each Session of b's home.
func (b testBridge) factory() agent.ExecutorFactory {
	factory := newViewExecutorFactory(b.Config, viewLayout{node: b.Node, bridge: b.Entrypoint})
	return func(ctx context.Context, req agent.PrepareRequest) (agent.Executor, error) {
		mcp, err := viewMCP(req)
		if err != nil {
			return nil, err
		}
		if req.LocalEnvironment != nil && req.WorkspaceRoot == "" {
			req.WorkspaceRoot = b.Workspace
		}
		return factory(ctx, req, agent.ViewSession{Home: agent.ViewDir{Host: b.Home, View: b.Home}, Proxy: testProxy, MCP: mcp,
			Launch: func(options clirunner.StartOptions) (*clirunner.Process, error) {
				if err := os.MkdirAll(options.Dir, 0o700); err != nil {
					return nil, err
				}
				options.Env = append(options.Env, b.Env...)
				return clirunner.Start(options)
			}})
	}
}

// startSingleTurn prepares an Executor as the registry does, starts one Turn
// and closes the Executor once that Turn settles, so each test observes the
// complete native lifecycle.
func startSingleTurn(ctx context.Context, config testBridge, req proto.PromptRequestPayload, run string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	configuration, err := Declaration.Configuration.Prepare(req)
	if err != nil {
		return nil, err
	}
	resource, err := config.factory()(ctx, agent.PrepareRequest{PreparationDeadline: time.Now().Add(time.Minute), PromptRequestPayload: req, Prepared: configuration})
	if err != nil {
		return nil, err
	}
	turn, err := resource.StartTurn(ctx, run, input, out)
	if turn == nil {
		_ = resource.Close(context.Background())
		return nil, err
	}
	go func() { _, _ = turn.AwaitSettlement(context.Background()); _ = resource.Close(context.Background()) }()
	return turn, err
}

func helperTurn(scanner *bufio.Scanner) (func(bridgeEvent), func()) {
	_ = json.NewEncoder(os.Stdout).Encode(bridgeEvent{Type: "executor_ready", Protocol: 4})
	if !scanner.Scan() {
		os.Exit(4)
	}
	var start struct {
		Type   string             `json:"type"`
		TurnID string             `json:"turn_id"`
		Input  proto.MessageInput `json:"input"`
	}
	if json.Unmarshal(scanner.Bytes(), &start) != nil || start.Type != "turn_start" || start.TurnID == "" {
		os.Exit(5)
	}
	return helperTurnOutput(scanner, start.TurnID)
}

func helperTurnOutput(scanner *bufio.Scanner, id string) (func(bridgeEvent), func()) {
	terminal := false
	reusable := true
	encode := func(event bridgeEvent) {
		event.TurnID = id
		_ = json.NewEncoder(os.Stdout).Encode(event)
		if event.Type == "result" || event.Type == "error" {
			terminal = true
			if event.Type == "error" && event.Code != "cancelled" {
				reusable = false
			}
		}
	}
	encode(bridgeEvent{Type: "turn_started"})
	return encode, func() {
		if !terminal {
			return
		}
		reason := ""
		if !reusable {
			reason = "native_error"
		}
		confirmed := true
		encode(bridgeEvent{Type: "turn_settled", Confirmed: &confirmed, Reusable: &reusable, Reason: reason})
		for scanner.Scan() {
		}
	}
}
