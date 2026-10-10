package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
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
		if snapshot.Environment.Type == "openai_hosted" {
			if err := w.runtimes.validateWorkspaceAdmission(ctx); err != nil {
				return err
			}
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

func (w *Worker) submitEnvironmentInputs(ctx context.Context, session sessions.Session, key string, inputs []sessions.Input) (receipts []sessions.InputReceipt, err error) {
	checkAt := time.Now()
	err = w.validateEnvironmentAdmission(ctx, session.Engine, session.Configuration)
	observeExecutionStage(ctx, "input_validate", checkAt, err, "session_id", session.ID)
	if err != nil {
		return nil, err
	}
	checkAt = time.Now()
	err = w.checkAdmissionOwnership(ctx)
	observeExecutionStage(ctx, "input_ownership", checkAt, err, "session_id", session.ID)
	if err != nil {
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
	changed, unsubscribe := w.dispatcher.notifications.subscribe(session.TenantID, session.ID)
	defer unsubscribe()
	reservedAt := time.Now()
	reserve, cancel := context.WithTimeout(ctx, 5*time.Second)
	reservation, err := w.dispatcher.Sessions.ReserveEnvironmentInput(reserve, session.TenantID, session.ID, key, inputs)
	cancel()
	observeExecutionStage(ctx, "input_reserve", reservedAt, err, "session_id", session.ID)
	if err != nil {
		return nil, err
	}
	obslog.Ctx(ctx).Info("environment input reserved", "session_id", session.ID,
		"reservation_id", reservation.ID, "execution_trace_id", reservationTraceID(reservation.ID).String(), "state", reservation.State)
	admittedAt := time.Now()
	defer func() {
		observeExecutionStage(ctx, "input_admission_wait", admittedAt, err,
			"session_id", session.ID, "reservation_id", reservation.ID, "state", reservation.State)
	}()
	w.wakeScheduler()
	if reservation.State == sessions.EnvironmentInputPending {
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
		return w.dispatcher.Sessions.ExpireEnvironmentInput(read, session.TenantID, session.ID, reservation.ID)
	}
	return w.dispatcher.SessionsReader.GetEnvironmentInputReservation(read, session.TenantID, session.ID, reservation.ID)
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

func (m *runtimeManager) validateWorkspaceAdmission(ctx context.Context) error {
	m.mu.Lock()
	config := m.config
	m.mu.Unlock()
	if config.Workspace == nil {
		return nil
	}
	if m.workspaces == nil {
		return workspacefs.ErrUnavailable
	}
	declaration, err := m.workspaces.Declaration(ctx)
	if err != nil {
		return err
	}
	if declaration == nil {
		return workspacefs.ErrUnavailable
	}
	if config.WorkspaceRequirements == nil {
		return workspacefs.ErrUnsupported
	}
	return workspacefs.ValidateCombination(*config.WorkspaceRequirements, *declaration, config.Resources.EnvironmentDiskMiB)
}
