package claudesdk

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type session struct {
	owner          *executor
	runID          string
	frames         chan []byte
	outputDone     chan struct{}
	turnSettlement agent.TurnSettlement
	settlementErr  error
	outputErr      error
	cancelOnce     sync.Once
	cancelOutput   chan struct{}
	nativeEnded    bool

	reads       workspaceReadState
	directories workspaceDirectoryState
	process     *clirunner.Process
	writeMu     *sync.Mutex
	functions   functionState
	steering    steeringState
	settled     chan struct{}
	outcome     proto.DonePayload
}

func NewFactory(config Config) agent.Factory {
	prepareExecutor := NewExecutorFactory(config)
	return func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		run, input := req.RunID, req.Input
		req.RunID, req.ConversationID, req.Input = "", "", nil
		resource, err := prepareExecutor(ctx, req)
		if err != nil {
			return nil, err
		}
		turn, err := resource.StartTurn(ctx, run, input, out)
		if turn == nil {
			_ = resource.Close(context.Background())
			return nil, err
		}
		// Factory callers own one execution. Both entrypoints use the same
		// Executor implementation; Runtime pooling uses NewExecutorFactory.
		go func() { _, _ = turn.AwaitSettlement(context.Background()); _ = resource.Close(context.Background()) }()
		return turn, err
	}
}

type bridgeEvent struct {
	EngineErrorCode json.RawMessage `json:"engine_error_code"`
	TurnID          string          `json:"turn_id"`
	Reusable        *bool           `json:"reusable"`
	Confirmed       *bool           `json:"confirmed"`
	Reason          string          `json:"reason"`
	Protocol        int             `json:"protocol"`

	Fact        json.RawMessage             `json:"fact"`
	InputID     string                      `json:"input_id"`
	ResultID    string                      `json:"result_id"`
	Usage       json.RawMessage             `json:"usage,omitempty"`
	Type        string                      `json:"type"`
	Delta       string                      `json:"delta"`
	SessionID   string                      `json:"session_id"`
	Text        string                      `json:"text"`
	Code        string                      `json:"code"`
	ItemID      string                      `json:"item_id"`
	Message     *proto.OutputMessagePayload `json:"message"`
	Call        *proto.FunctionCallPayload  `json:"call"`
	CallID      string                      `json:"call_id"`
	DeliveryID  string                      `json:"delivery_id"`
	ID          string                      `json:"id"`
	Stage       string                      `json:"stage"`
	Observation *proto.ToolObservation      `json:"observation"`
}

func launch(ctx context.Context, config Config, start startRequest, env []string) (*session, error) {
	binary := config.Node
	if binary == "" {
		binary = "node"
	}
	process, err := clirunner.Start(clirunner.StartOptions{Parent: ctx, Binary: binary, Args: []string{config.Entrypoint}, Dir: start.Cwd, Env: env, NeedStdin: true, OwnProcessGroup: true})
	if err != nil {
		return nil, err
	}
	return &session{process: process, writeMu: &sync.Mutex{}, functions: functionState{calls: map[string]*pendingFunction{}}, settled: make(chan struct{})}, nil
}

func (s *session) drain(scanner *bridgeOutput, stderrDone <-chan struct{}, failure error) (error, bool) {
	for scanner.Scan() {
	}
	if scanner.Err() != nil {
		failure = fmt.Errorf("claudesdk: SDK bridge output read failed")
		s.process.Cancel()
	}
	<-stderrDone
	s.stopWorkspaceReads()
	s.stopWorkspaceDirectories()
	waitErr := s.process.Wait()
	if waitErr != nil && failure == nil {
		failure = fmt.Errorf("claudesdk: SDK process failed")
	}
	return failure, scanner.Err() == nil && waitErr == nil
}

func bridgeFailure(code string) error {
	switch code {
	case "invalid_request", "history_unavailable", "execution_failed", "cancelled":
		return fmt.Errorf("claudesdk: %s", code)
	default:
		return fmt.Errorf("claudesdk: unknown SDK bridge failure")
	}
}
