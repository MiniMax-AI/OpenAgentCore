package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type EnvironmentRun struct {
	Reservation sessions.EnvironmentInputReservation
	Turn        sessions.Turn
}

// RunEnvironmentInput reserves a Turn on the Session-owned Runtime Executor. It
// checks lease, the lease the Dispatcher's execution operations hold, before
// any Runtime preparation.
func (d *Dispatcher) RunEnvironmentInput(ctx context.Context, lease Ownership, tenantID, sessionID, reservationID string) (run EnvironmentRun, err error) {
	if err = lease.CheckOwnership(ctx); err != nil {
		return run, err
	}
	expire, cancel := context.WithTimeout(ctx, 5*time.Second)
	run.Reservation, err = d.Sessions.ExpireEnvironmentInput(expire, tenantID, sessionID, reservationID)
	cancel()
	if err != nil || run.Reservation.State != sessions.EnvironmentInputPending {
		return run, err
	}
	session, err := d.SessionsReader.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return run, err
	}
	environment, err := d.SessionsReader.GetSessionEnvironment(ctx, tenantID, sessionID)
	if err != nil {
		return run, err
	}
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil || strings.TrimSpace(snapshot.Agent.Model) == "" {
		return run, sessions.ErrInvalidInput
	}
	bound, err := d.SessionsReader.GetSessionExecutionBinding(ctx, tenantID, sessionID)
	if err != nil {
		return run, err
	}
	peer, err := d.authorizedPeer(ctx, bound.Device.ID)
	if err != nil {
		return run, err
	}
	caps, err := d.engineCapabilities(peer, session.Engine, snapshot)
	if err != nil {
		return run, err
	}
	owner, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := d.executionRequest(owner, session, snapshot, caps, bound)
	if err != nil {
		return run, err
	}
	var messages proto.MessageInput
	for _, input := range run.Reservation.Inputs {
		if input.Kind != "message" {
			return run, sessions.ErrInvalidInput
		}
		text, err := messageInput(input.Payload)
		if err != nil {
			return run, err
		}
		messages = append(messages, text...)
	}
	if err := d.messageInputSupport(peer, session.Engine, snapshot, messages); err != nil {
		return run, err
	}
	if err := d.configurePreparedEnvironment(session, environment, bound.Device, &req); err != nil {
		return run, err
	}
	prepared, err := newPreparedStart(peer)
	if err != nil {
		return run, err
	}
	defer prepared.close()
	if err = send(owner, peer, proto.TypeExecutionPrepare, prepared.requestID, proto.ExecutionPreparePayload{SessionID: sessionID, Configuration: req}); err != nil {
		return run, err
	}
	run.Reservation, err = d.awaitPreparation(owner, tenantID, sessionID, run.Reservation, prepared)
	if err != nil || run.Reservation.State != sessions.EnvironmentInputPending {
		return run, err
	}
	if err := d.messageInputSupport(peer, session.Engine, snapshot, messages); err != nil {
		return run, err
	}
	promoted, err := d.sessionExecution.PromoteEnvironmentInput(owner, tenantID, sessionID, reservationID)
	if errors.Is(err, sessions.ErrTurnConflict) {
		// A rejected claim leaves the reservation pending for a later attempt.
		return run, err
	}
	if err == nil {
		d.notifications.notify(tenantID, sessionID)
	}
	run.Reservation = promoted
	if err != nil || run.Reservation.State != sessions.EnvironmentInputAdmitted {
		return run, err
	}
	if run.Reservation.Receipts[0].Replayed {
		return run, nil
	}
	turnID := run.Reservation.Receipts[0].TurnID
	through := run.Reservation.Receipts[len(run.Reservation.Receipts)-1].Sequence
	releaseDelivery, err := peer.TrackExecutionDelivery(turnID)
	if err != nil {
		run.Turn, err = d.finishRun(tenantID, sessionID, turnID, snapshot.Agent.Model, Result{ErrorCode: "delivery_unknown", AppliedThrough: through}, sessions.TurnFailed)
		return run, err
	}
	defer releaseDelivery()
	result, status := d.deliver(owner, tenantID, sessionID, peer, req, turnID, messages, through, prepared)
	result, status = d.captureCompletedArtifacts(owner, peer, session, environment, bound.Device, turnID, result, status)
	run.Turn, err = d.finishRun(tenantID, sessionID, turnID, snapshot.Agent.Model, result, status)
	return run, err
}
