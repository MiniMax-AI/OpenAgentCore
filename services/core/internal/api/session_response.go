package api

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// WithEnvironmentRemoteURL uses the composition's validated daemon executor URL for self-hosted requests and output.
func WithEnvironmentRemoteURL(origin string) Option {
	return func(h *Handler) { h.executorURL = origin }
}

func sessionResponse(session store.Session, executorURL string) (v1.Session, error) {
	var cfg configuration
	if err := json.Unmarshal(session.Configuration, &cfg); err != nil || cfg.Agent.ID == "" || cfg.Agent.Model == "" {
		return v1.Session{}, errors.New("unsupported stored session configuration")
	}
	if cfg.Agent.XAgentsCore != nil && session.Engine != "" {
		cfg.Agent.XAgentsCore = &v1.AgentsCore{Harness: session.Engine, HarnessConfig: cfg.Agent.XAgentsCore.HarnessConfig}
	}
	// The pinned Session AgentTool resource union excludes the tool_search
	// input declaration. Retain it in saved Agents and frozen execution input.
	tools := make([]json.RawMessage, 0, len(cfg.Agent.Tools))
	for _, raw := range cfg.Agent.Tools {
		var tool struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			return v1.Session{}, errors.New("unsupported stored tool configuration")
		}
		if tool.Type != "tool_search" {
			tools = append(tools, projectedMCPCredential(raw, cfg))
		}
	}
	cfg.Agent.Tools = tools
	environment, err := sessionEnvironment(session, cfg.Environment.Type, executorURL)
	if err != nil {
		return v1.Session{}, err
	}
	response := v1.Session{
		ID: session.ID, Agent: cfg.Agent, Environment: environment, Usage: tokenUsage(session.Usage),
		CreatedAt: session.CreatedAt.Unix(), LastActiveAt: session.CreatedAt.Unix(),
		Metadata: session.Metadata, Object: "agent.session", Status: "idle",
		RequiredActions: []v1.RequiredAction{}, VaultIDs: append([]string{}, cfg.VaultIDs...),
	}
	if turn := session.LastTurn; turn != nil {
		active := turn.CreatedAt
		if turn.StartedAt.After(active) {
			active = turn.StartedAt
		}
		if turn.CompletedAt.After(active) {
			active = turn.CompletedAt
		}
		response.LastActiveAt = active.Unix()
		switch turn.Status {
		case store.TurnQueued, store.TurnInProgress, store.TurnWaiting:
			response.Status = "in_progress"
			if turn.CancelRequestedAt.IsZero() && len(session.RequiredActions) > 0 {
				response.Status = "requires_action"
				for _, action := range session.RequiredActions {
					response.RequiredActions = append(response.RequiredActions, v1.RequiredAction{
						Type: action.Type, Arguments: action.Arguments, CallID: action.CallID, Name: action.Name, TurnID: action.TurnID,
					})
				}
			}
		case store.TurnFailed:
			response.Status = "failed"
			message := "The execution could not complete."
			response.Error = &message
		}
	}
	if activity := session.EnvironmentInputActivity; activity != nil {
		if activity.Status != "idle" && activity.Status != "requires_action" && activity.Status != "failed" {
			return v1.Session{}, errors.New("unsupported stored environment input activity")
		}
		response.Status, response.Error = activity.Status, nil
		if activity.Status == "failed" {
			message := "The initial input timed out waiting for the environment connection."
			switch activity.Failure {
			case "environment_unavailable":
				message = "The environment is no longer available for this input."
			case "runtime_preparation_failed":
				message = "Runtime preparation failed before execution. Check the daemon logs and installed capabilities, then submit new input."
			case "model_provider_required":
				message = "This Session was created without a model provider and cannot run. Create a new Session with x_agents_core.model_provider or an Agent that has one saved."
			}
			response.Error = &message
		}
		response.LastActiveAt = activity.LastActiveAt.Unix()
		response.RequiredActions = []v1.RequiredAction{}
		if activity.Status == "requires_action" {
			if activity.EnvironmentID == "" || activity.EnvironmentID != environment.ID {
				return v1.Session{}, errors.New("invalid stored environment connection action")
			}
			response.RequiredActions = append(response.RequiredActions, v1.RequiredAction{Type: "environment_connection", EnvironmentID: activity.EnvironmentID})
		}
	}
	// A hosted provisioning failure is terminal and supersedes the settled input
	// activity: the Session reports its safe reason and failure time.
	if failure := session.EnvironmentFailure; failure != nil {
		if cfg.Environment.Type != "openai_hosted" && cfg.Environment.Type != "self_hosted" {
			return v1.Session{}, errors.New("unsupported stored environment failure")
		}
		reason := failure.Reason
		response.Status, response.Error, response.LastActiveAt = "failed", &reason, failure.FailedAt.Unix()
		response.RequiredActions = []v1.RequiredAction{}
	}
	return response, nil
}

func sessionEnvironment(session store.Session, kind, executorURL string) (v1.SessionEnvironment, error) {
	if kind == "none" {
		return v1.SessionEnvironment{Type: "none"}, nil
	}
	environment := session.Environment
	if (kind != "self_hosted" && kind != "openai_hosted") || environment == nil || environment.ID == "" || environment.SessionID != session.ID || environment.TenantID != session.TenantID {
		return v1.SessionEnvironment{}, errors.New("unsupported stored session environment")
	}
	if kind == "openai_hosted" {
		return hostedSessionEnvironment(*environment)
	}
	if executorURL == "" {
		return v1.SessionEnvironment{}, errors.New("self-hosted executor origin unavailable")
	}
	var cfg struct {
		Type                  string   `json:"type"`
		CapabilityDirectories []string `json:"capability_directories"`
		WorkspaceDirectory    string   `json:"workspace_directory"`
	}
	if err := json.Unmarshal(environment.Configuration, &cfg); err != nil || cfg.Type != kind || cfg.WorkspaceDirectory == "" {
		return v1.SessionEnvironment{}, errors.New("unsupported stored session environment configuration")
	}
	if cfg.CapabilityDirectories == nil {
		cfg.CapabilityDirectories = []string{}
	}
	return v1.SessionEnvironment{
		Type: kind, ID: environment.ID, CapabilityDirectories: &cfg.CapabilityDirectories,
		RemoteURL: executorURL, WorkspaceDirectory: cfg.WorkspaceDirectory,
	}, nil
}
