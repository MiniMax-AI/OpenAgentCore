package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var (
	ErrEnvironmentInputExpired   = errors.New("environment input expired before admission")
	ErrEnvironmentInputCancelled = errors.New("environment input cancelled before admission")
	ErrExecutionUnavailable      = errors.New("execution ownership is unavailable")
)

func preparedEnvironmentConfiguration(configuration json.RawMessage) bool {
	var snapshot Snapshot
	return json.Unmarshal(configuration, &snapshot) == nil && snapshot.Environment != nil &&
		(snapshot.Environment.Type == "self_hosted" || snapshot.Environment.Type == "openai_hosted")
}

func (w *Worker) validateEnvironmentAdmission(ctx context.Context, engine string, configuration json.RawMessage) error {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil || snapshot.Environment == nil {
		return sessions.ErrInvalidInput
	}
	switch snapshot.Environment.Type {
	case "self_hosted":
		if w.dispatcher.Registry == nil {
			return sessions.ErrInvalidInput
		}
	case "openai_hosted":
		if w.runtimes == nil {
			return ErrExecutionUnavailable
		}
		ready, err := w.runtimes.ensureDeployment(ctx)
		if err != nil {
			return err
		}
		if !ready {
			return ErrExecutionUnavailable
		}
	default:
		return sessions.ErrInvalidInput
	}
	return w.dispatcher.ValidateSessionConfiguration(engine, configuration)
}

func (w *Worker) validateCreation(ctx context.Context, input sessions.CreateSession) error {
	if err := w.dispatcher.validateEngineInputs(input.Engine, input.Configuration, input.InitialInputs); err != nil {
		return err
	}
	if preparedEnvironmentConfiguration(input.Configuration) {
		if err := w.validateEnvironmentAdmission(ctx, input.Engine, input.Configuration); err != nil {
			return err
		}
		var snapshot Snapshot
		if err := json.Unmarshal(input.Configuration, &snapshot); err != nil {
			return sessions.ErrInvalidInput
		}
		if len(input.InitialInputs) == 0 && snapshot.Environment.Type == "self_hosted" {
			return nil
		}
		return w.checkAdmissionOwnership(ctx)
	}
	if !w.dispatcher.canAdmitInputs(input.Engine, input.Configuration) {
		return sessions.ErrInvalidInput
	}
	return nil
}

func (w *Worker) submitEnvironmentInputs(ctx context.Context, session sessions.Session, key string, inputs []sessions.Input) ([]sessions.InputReceipt, error) {
	if err := w.validateEnvironmentAdmission(ctx, session.Engine, session.Configuration); err != nil {
		return nil, err
	}
	if err := w.checkAdmissionOwnership(ctx); err != nil {
		return nil, err
	}
	kind := ""
	if len(inputs) > 0 {
		kind = inputs[0].Kind
	}
	if (kind == "cancel" || kind == "tool_result") && !slices.ContainsFunc(inputs, func(input sessions.Input) bool { return input.Kind != kind }) {
		// Neither kind creates a Turn. The Session lock preserves target and retry identity.
		return w.admitInputs(ctx, session.TenantID, session.ID, key, inputs)
	}
	// Messages start work. A Session from before deployment defaults moved into
	// Core may have no frozen provider; reject it here instead of queueing work
	// its harness cannot run. Cancellation and results above stay available.
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil || !snapshot.ModelProviderConfigured {
		return nil, ErrModelProviderRequired
	}
	changed, unsubscribe := w.dispatcher.notifications.subscribe(session.TenantID, session.ID)
	defer unsubscribe()
	reserve, cancel := context.WithTimeout(ctx, 5*time.Second)
	reservation, err := w.admission.ReserveEnvironmentInput(reserve, session.TenantID, session.ID, key, inputs)
	cancel()
	if err != nil {
		return nil, err
	}
	w.wakeScheduler()
	if reservation.State == sessions.EnvironmentInputPending && !reservation.IsInitial {
		w.hintRuntimeWake(ctx, session)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		switch reservation.State {
		case sessions.EnvironmentInputAdmitted:
			return reservation.Receipts, nil
		case sessions.EnvironmentInputFailed:
			return nil, sessions.ErrEnvironmentUnavailable
		case sessions.EnvironmentInputExpired:
			return nil, ErrEnvironmentInputExpired
		case sessions.EnvironmentInputCancelled:
			return nil, ErrEnvironmentInputCancelled
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		case <-changed:
		}
		if err := w.checkAdmissionOwnership(ctx); err != nil {
			return nil, err
		}
		reservation, err = w.environmentInputOutcome(ctx, session, reservation)
		if err != nil {
			return nil, err
		}
	}
}

func (w *Worker) environmentInputOutcome(ctx context.Context, session sessions.Session, reservation sessions.EnvironmentInputReservation) (sessions.EnvironmentInputReservation, error) {
	read, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// The database rechecks its clock under the Session lock before settlement.
	if !time.Now().Before(reservation.Deadline) {
		return w.admission.ExpireEnvironmentInput(read, session.TenantID, session.ID, reservation.ID)
	}
	return w.admission.GetEnvironmentInputReservation(read, session.TenantID, session.ID, reservation.ID)
}

func (w *Worker) checkAdmissionOwnership(ctx context.Context) error {
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.CheckOwnership(check); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrExecutionUnavailable
	}
	return nil
}
