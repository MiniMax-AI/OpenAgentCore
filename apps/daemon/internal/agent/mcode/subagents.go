package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type nativeSubagentSnapshot struct {
	Version  int                     `json:"version"`
	Root     string                  `json:"rootSessionId"`
	Complete bool                    `json:"complete"`
	Sessions []nativeSubagentSession `json:"sessions"`
}
type nativeSubagentSession struct {
	ID, Parent, Name, Kind, Purpose, Status string
	CreatedAt                               int64
	Turns                                   []nativeSubagentTurn
	Messages                                []nativeSubagentMessage
	Tasks                                   []nativeSubagentTask
}
type nativeSubagentTurn struct {
	ID, Status  string
	CreatedAt   int64
	CompletedAt *int64
}
type nativeSubagentMessage struct {
	ID, TurnID, Role string
	CreatedAt        int64
	Data             struct {
		Text     string               `json:"msg_content"`
		Thinking string               `json:"thinking_content"`
		Tools    []nativeSubagentTool `json:"tool_calls"`
	}
}
type nativeSubagentTool struct {
	ID     string `json:"tool_call_id"`
	Name   string `json:"tool_name"`
	Status int    `json:"tool_call_status"`
	Args   string `json:"tool_call_args"`
	Result string `json:"tool_call_result_data"`
}
type nativeSubagentTask struct {
	TaskID, ToolCallID, OwnerSessionID, Status string
	Metadata                                   struct{ ChildSessionID, ParentSessionID, ParentTurnID, SubTurnID, ExecutionMode string }
}

func subagentReader() (string, string, error) {
	node, bridge := os.Getenv("OAC_RUNTIME_MCODE_NODE"), os.Getenv("OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE")
	reader := filepath.Join(filepath.Dir(bridge), "subagent-snapshot.mjs")
	for _, path := range []string{node, bridge, reader} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(path) || resolved != path {
			return "", "", fmt.Errorf("mcode: protected Subagent reader is unavailable")
		}
	}
	return node, reader, nil
}

func (s *Session) readSubagents(ctx context.Context) (nativeSubagentSnapshot, error) {
	var snapshot nativeSubagentSnapshot
	node, reader, err := subagentReader()
	if err != nil {
		return snapshot, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--disable-warning=ExperimentalWarning", reader, s.opts.DataDir, s.sessionID)
	command.Env = executionEnvironment()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return snapshot, fmt.Errorf("mcode: child history reader is unavailable")
	}
	if err := command.Start(); err != nil {
		return snapshot, fmt.Errorf("mcode: child history reader is unavailable")
	}
	raw, readErr := io.ReadAll(io.LimitReader(stdout, 64*1024*1024+1))
	if readErr != nil || len(raw) > 64*1024*1024 {
		_ = command.Process.Kill()
	}
	err = command.Wait()
	if err != nil || readErr != nil || len(raw) > 64*1024*1024 {
		return snapshot, fmt.Errorf("mcode: complete child history is unavailable")
	}
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Version != 1 || !snapshot.Complete || snapshot.Root != s.sessionID {
		return snapshot, fmt.Errorf("mcode: invalid child history snapshot")
	}
	return snapshot, nil
}

func (s *Session) settleSubagents() error {
	if s.req.DisableSubagents {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	for {
		snapshot, err := s.readSubagents(ctx)
		if err != nil {
			return err
		}
		settled := true
		for _, session := range snapshot.Sessions {
			if session.ID == snapshot.Root && s.rootCompletedAtMS == nil {
				for _, turn := range session.Turns {
					if !s.previousNativeTurns[turn.ID] && turn.Status == "completed" && turn.CompletedAt != nil {
						s.rootCompletedAtMS = turn.CompletedAt
						break
					}
				}
			}
			for _, task := range session.Tasks {
				if task.Status == "queued" || task.Status == "running" || task.Status == "stopping" {
					settled = false
				}
			}
			for _, turn := range session.Turns {
				if turn.Status == "accepted" {
					settled = false
				}
			}
		}
		if settled {
			return s.projectSubagents(snapshot)
		}
		// Root output is frozen. Drain native notifications while its existing
		// background task owners finish; none becomes a root output delta.
		select {
		case <-ctx.Done():
			return fmt.Errorf("mcode: child settlement did not complete")
		case _, ok := <-s.frames:
			if !ok {
				return fmt.Errorf("mcode: native owner ended before child settlement")
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *Session) projectSubagents(snapshot nativeSubagentSnapshot) error {
	byID := map[string]nativeSubagentSession{}
	tasks := map[string]nativeSubagentTask{}
	for _, session := range snapshot.Sessions {
		if session.ID == "" || byID[session.ID].ID != "" {
			return fmt.Errorf("mcode: duplicate native child identity")
		}
		byID[session.ID] = session
		for _, task := range session.Tasks {
			if task.Metadata.ExecutionMode != "append" && task.Metadata.ChildSessionID != "" {
				tasks[task.Metadata.ChildSessionID] = task
			}
		}
	}
	emitted := map[string]bool{snapshot.Root: true}
	for len(emitted) < len(byID) {
		progress := false
		for _, session := range snapshot.Sessions {
			if emitted[session.ID] || !emitted[session.Parent] {
				continue
			}
			task, ok := tasks[session.ID]
			if !ok || session.Kind != "task" || session.CreatedAt <= 0 || task.OwnerSessionID != session.Parent || task.Metadata.ParentTurnID == "" || task.ToolCallID == "" {
				return fmt.Errorf("mcode: native child provenance is incomplete")
			}
			name := session.Name
			s.emit(proto.TypeSubagentIdentity, proto.SubagentIdentityPayload{NativeID: session.ID, ParentNativeID: session.Parent, NativeCreatedAt: session.CreatedAt / 1000, ParentTurnID: task.Metadata.ParentTurnID, SourceItemID: task.ToolCallID, Name: &name})
			emitted[session.ID], progress = true, true
		}
		if !progress {
			return fmt.Errorf("mcode: native child graph is incomplete")
		}
	}
	for _, session := range snapshot.Sessions {
		if session.ID == snapshot.Root {
			for _, message := range session.Messages {
				if s.previousNativeTurns[message.TurnID] {
					continue
				}
				for _, tool := range message.Data.Tools {
					value, err := nativeCoordination(session, tool)
					if err != nil {
						return err
					}
					if value != nil {
						value.ActorID = ""
						s.emit(proto.TypeSubagentCoordination, value)
					}
				}
			}
			continue
		}
		for _, turn := range session.Turns {
			if turn.ID == "" || turn.CreatedAt <= 0 {
				return fmt.Errorf("mcode: invalid child Turn")
			}
			status := map[string]string{"accepted": "in_progress", "completed": "completed", "failed": "failed", "aborted": "cancelled"}[turn.Status]
			if status == "" || (status != "in_progress" && turn.CompletedAt == nil) {
				return fmt.Errorf("mcode: incomplete child Turn boundary")
			}
			value := proto.SubagentTurnPayload{NativeID: session.ID, TurnID: turn.ID, Status: "in_progress", CreatedAtMS: turn.CreatedAt}
			s.emit(proto.TypeSubagentTurn, value)
			if err := s.projectSubagentMessages(session, turn); err != nil {
				return err
			}
			value.Status, value.CompletedAtMS = status, turn.CompletedAt
			s.emit(proto.TypeSubagentTurn, value)
		}
	}
	return nil
}
