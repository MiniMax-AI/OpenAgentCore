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
	turn, err := w.dispatcher.SessionsReader.GetTurn(ctx, item.TenantID, item.SessionID, item.TurnID)
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
	_, err = w.dispatcher.sessionExecution.TransitionTurn(ctx, item.TenantID, item.SessionID, item.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_device_unavailable"}`)})
	if errors.Is(err, sessions.ErrTurnConflict) {
		err = nil
	}
	return false, err
}

func (w *Worker) bindDevice(ctx context.Context, tenantID, sessionID string, input proto.MessageInput) (bool, error) {
	session, err := w.dispatcher.SessionsReader.GetSession(ctx, tenantID, sessionID)
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
		peer, err := w.dispatcher.authorizedPeer(ctx, id)
		if err != nil {
			return false
		}
		_, err = admitSession(peer, session.Engine, snapshot, input)
		return err == nil
	})
}

// bindSessionDevice places the Session for a Turn or Environment input. A
// hosted or self_hosted Session is placed once its Environment completed
// initialization and while its compute is not quiesced; the initialization
// scanner places it before that.
func (w *Worker) bindSessionDevice(ctx context.Context, session sessions.Session, ready func(string) bool) (bool, error) {
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil {
		return false, sessions.ErrInvalidInput
	}
	if snapshot.Environment == nil || (snapshot.Environment.Type != "openai_hosted" && snapshot.Environment.Type != "self_hosted") {
		return w.place(ctx, session.TenantID, session.ID, "", ready)
	}
	environment, err := w.dispatcher.SessionsReader.GetSessionEnvironment(ctx, session.TenantID, session.ID)
	if err != nil {
		return false, err
	}
	if _, err := parseEnvironmentPlacement(environment.Configuration); err != nil || environment.Initialization != "complete" {
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
	return w.place(ctx, session.TenantID, session.ID, environment.ID, ready)
}

// place reports whether the Session's bound Runtime is ready and, for a
// Session with an Environment, the Environment's Link resource is Serving.
// A Session without an assignment is bound to the first ready agent host once
// its Environment, if any, is Serving.
func (w *Worker) place(ctx context.Context, tenant, session, environment string, ready func(string) bool) (bool, error) {
	if environment != "" {
		serving, err := w.dispatcher.environmentServing(ctx, tenant, environment)
		if err != nil || !serving {
			return false, err
		}
	}
	bound, err := w.dispatcher.SessionsReader.GetSessionRuntimeDevice(ctx, tenant, session)
	if err == nil {
		return ready(bound.ID), nil
	}
	if !errors.Is(err, sessions.ErrNotFound) {
		return false, err
	}
	hosts, err := w.dispatcher.SessionsReader.ListAgentHosts(ctx)
	if err != nil {
		return false, err
	}
	for _, host := range hosts {
		if !ready(host.ID) {
			continue
		}
		err := w.dispatcher.sessionExecution.BindSessionDevice(ctx, tenant, session, host.ID)
		return err == nil, err
	}
	return false, nil
}

// environmentServing reports whether the relay holds the serve peer of the
// tenant's Environment's live Link resource.
func (d *Dispatcher) environmentServing(ctx context.Context, tenant, environment string) (bool, error) {
	resource, err := d.SessionsReader.GetEnvironmentResource(ctx, tenant, environment)
	if errors.Is(err, sessions.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return d.Links.Serving(resource.Resource.Ref()), nil
}
