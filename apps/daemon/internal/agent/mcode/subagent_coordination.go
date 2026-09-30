package mcode

import (
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func nativeCoordination(session nativeSubagentSession, tool nativeSubagentTool) (*proto.SubagentCoordinationPayload, error) {
	if tool.Status != 2 || (tool.Name != "task" && tool.Name != "task_append") {
		return nil, nil
	}
	var args struct {
		Prompt, Content string
		TaskID          string `json:"task_id"`
	}
	if json.Unmarshal([]byte(tool.Args), &args) != nil {
		return nil, fmt.Errorf("mcode: invalid delegation arguments")
	}
	value := &proto.SubagentCoordinationPayload{ID: tool.ID, ActorID: session.ID, Status: "completed"}
	if tool.Name == "task" {
		value.Kind, value.Text = "create_subagent_call", &args.Prompt
		for _, task := range session.Tasks {
			if task.ToolCallID == tool.ID && task.Metadata.ExecutionMode != "append" {
				value.Recipients = []string{task.Metadata.ChildSessionID}
				break
			}
		}
	} else {
		var result struct {
			Details struct {
				Accepted bool   `json:"accepted"`
				TaskID   string `json:"task_id"`
			} `json:"details"`
		}
		if json.Unmarshal([]byte(tool.Result), &result) != nil || !result.Details.Accepted {
			return nil, fmt.Errorf("mcode: delegation receipt is missing")
		}
		value.Kind, value.Text = "send_subagent_input_call", &args.Content
		for _, task := range session.Tasks {
			if task.TaskID == result.Details.TaskID {
				value.Recipients = []string{task.Metadata.ChildSessionID}
				break
			}
		}
	}
	if len(value.Recipients) != 1 || value.Recipients[0] == "" {
		return nil, fmt.Errorf("mcode: successful delegation lacks child identity")
	}
	return value, nil
}
