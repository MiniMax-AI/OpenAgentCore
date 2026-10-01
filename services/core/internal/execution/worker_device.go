package execution

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (w *Worker) bind(ctx context.Context, item sessions.ExecutionWork) (bool, error) {
	input, _, inputErr := w.dispatcher.initialInput(ctx, item.TenantID, item.SessionID, item.TurnID)
	if inputErr != nil && !errors.Is(inputErr, sessions.ErrInvalidInput) && !errors.Is(inputErr, sessions.ErrNotFound) {
		return false, inputErr
	}
	// Candidate selection is a snapshot. Cancellation can append a control input
	// before this read, so recheck eligibility after reading the input history.
	turn, err := w.dispatcher.Store.GetTurn(ctx, item.TenantID, item.SessionID, item.TurnID)
	if errors.Is(err, sessions.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if turn.Status != sessions.TurnQueued || !turn.CancelRequestedAt.IsZero() {
		return false, nil
	}
	if inputErr != nil {
		return false, inputErr
	}
	ready, err := w.bindDevice(ctx, item.TenantID, item.SessionID, input)
	if errors.Is(err, sessions.ErrNotFound) {
		return false, nil
	}
	if !errors.Is(err, sessions.ErrDeviceBindingConflict) {
		return ready, err
	}
	_, err = w.dispatcher.Store.TransitionTurn(ctx, item.TenantID, item.SessionID, item.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_device_unavailable"}`)})
	if errors.Is(err, sessions.ErrTurnConflict) {
		err = nil
	}
	return false, err
}

func (w *Worker) bindDevice(ctx context.Context, tenantID, sessionID string, input proto.MessageInput) (bool, error) {
	session, err := w.dispatcher.Store.GetSession(ctx, tenantID, sessionID)
	if errors.Is(err, sessions.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(session.Configuration, &snapshot); err != nil {
		return false, err
	}
	return w.bindSessionDevice(ctx, session, func(id string) bool {
		if !w.ready(ctx, id, session.Engine, snapshot) {
			return false
		}
		if !input.HasImages() {
			return true
		}
		peer, err := w.dispatcher.authorizedPeer(ctx, id)
		return err == nil && w.dispatcher.messageInputSupport(peer, session.Engine, snapshot, input) == nil
	})
}

func (w *Worker) bindSessionDevice(ctx context.Context, session sessions.Session, ready func(string) bool) (bool, error) {
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil {
		return false, sessions.ErrInvalidInput
	}
	if snapshot.Environment != nil && (snapshot.Environment.Type == "openai_hosted" || snapshot.Environment.Type == "self_hosted") {
		environment, err := w.dispatcher.SessionsReader.GetSessionEnvironment(ctx, session.TenantID, session.ID)
		if err != nil {
			return false, err
		}
		if _, err := parseEnvironmentPlacement(environment.Configuration); err != nil {
			return false, nil
		}
		allocation, err := w.dispatcher.DeploymentReader.EnvironmentAllocation(ctx, deployment.AllocationKey{TenantID: session.TenantID, EnvironmentID: environment.ID})
		if errors.Is(err, deployment.ErrNotFound) {
			if snapshot.Environment.Type == "openai_hosted" {
				return false, nil
			}
		} else if err != nil {
			return false, err
		} else if allocation.ComputePhase != "disabled" && allocation.ComputePhase != "running" {
			// A reconnect authenticates transport before the retained Environment
			// resumes. Its first control frame must remain the lifecycle's Resume.
			return false, nil
		}
		bound, err := w.dispatcher.SessionsReader.GetSessionDevice(ctx, session.TenantID, session.ID)
		if errors.Is(err, sessions.ErrNotFound) {
			return false, nil
		}
		return err == nil && environmentDeviceMatches(session, environment, bound) && ready(bound.ID), err
	}
	bound, err := w.dispatcher.SessionsReader.GetSessionDevice(ctx, session.TenantID, session.ID)
	if err == nil {
		return bound.EnvironmentID == "" && ready(bound.ID), nil
	}
	if !errors.Is(err, sessions.ErrNotFound) {
		return false, err
	}
	devices, err := w.dispatcher.Store.ListExecutionDevices(ctx, session.TenantID)
	if err != nil {
		return false, err
	}
	for _, device := range devices {
		if !ready(device.ID) {
			continue
		}
		err := w.dispatcher.sessionExecution.BindSessionDevice(ctx, session.TenantID, session.ID, device.ID)
		return err == nil, err
	}
	return false, nil
}

func (w *Worker) ready(ctx context.Context, deviceID, engine string, snapshot Snapshot) bool {
	peer, err := w.dispatcher.authorizedPeer(ctx, deviceID)
	if err != nil {
		return false
	}
	_, err = w.dispatcher.engineCapabilities(peer, engine, snapshot)
	return err == nil
}
