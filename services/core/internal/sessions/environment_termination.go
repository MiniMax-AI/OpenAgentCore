package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// EnvironmentType returns the type an Environment configuration snapshot
// names: self_hosted or openai_hosted.
func EnvironmentType(configuration json.RawMessage) (string, error) {
	var config struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(configuration, &config); err != nil || (config.Type != "self_hosted" && config.Type != "openai_hosted") {
		return "", errors.New("invalid stored Environment type")
	}
	return config.Type, nil
}

// EnvironmentStateChange is the pinned agent.session.environment.<status>
// event for an Environment status the transaction has written. A failure
// carries the observed official connection error; the failed step travels only
// in the separate error event and the Session's error.
func EnvironmentStateChange(environment, kind, status string) SessionChange {
	state := &v1.SessionEnvironmentState{ID: environment, Type: kind, Status: status}
	if status == "failed" {
		state.Error = &v1.StreamError{Type: "environment_error", Code: "environment_connection_failed", Message: "The environment failed to connect."}
	}
	return SessionChange{Event: v1.SessionEvent{Type: "agent.session.environment." + status, Environment: state}}
}

// environmentFailedChanges end a Session whose Environment failed: an error
// event carrying the safe reason, then one agent.session.failed snapshot of
// the input activity, the Session's usage and the failure, matching later
// Session reads.
func environmentFailedChanges(failure EnvironmentFailure, activity *EnvironmentInputActivity, pending bool, usage json.RawMessage) []SessionChange {
	return []SessionChange{
		{Event: v1.SessionEvent{Type: "error", Error: &v1.StreamError{Type: "environment_error", Code: "sandbox_error", Message: failure.Reason}}},
		{
			Event:                    v1.SessionEvent{Type: "agent.session.failed"},
			EnvironmentInputActivity: activity, EnvironmentFailure: &EnvironmentFailure{Reason: failure.Reason, FailedAt: failure.FailedAt},
			SessionUsage: usage, Settled: !pending,
		},
	}
}

// EnvironmentFailureTx is the Session transaction FailEnvironment runs in.
type EnvironmentFailureTx interface {
	CancellationTx
	InputActivityTx
	// RecordEnvironmentFailure marks the Session's live Environment failed
	// with the reason and the private detail, and returns the failure time the
	// database clock recorded.
	RecordEnvironmentFailure(ctx context.Context, environment, reason string, detail *ProvisioningFailureDetail) (time.Time, error)
	// FailPendingInput settles the Session's pending Environment input
	// reservation as failed.
	FailPendingInput(ctx context.Context) error
}

// FailEnvironment records the failure of the Session's live environment and
// ends the Session failed, in the observed official order:
// agent.session.environment.failed, the changes of cancelling the Session's
// work, an error event with the safe reason, then one agent.session.failed
// snapshot. Pending input settles as failed first, so the snapshot reports the
// settled input activity.
func FailEnvironment(ctx context.Context, tx EnvironmentFailureTx, environment Environment, reason string, detail *ProvisioningFailureDetail) error {
	failedAt, err := tx.RecordEnvironmentFailure(ctx, environment.ID, reason, detail)
	if err != nil {
		return err
	}
	kind, err := EnvironmentType(environment.Configuration)
	if err != nil {
		return err
	}
	if environment.AttachedSessionID == "" {
		return nil
	}
	if err := tx.AppendChanges(ctx, EnvironmentStateChange(environment.ID, kind, "failed")); err != nil {
		return err
	}
	if err := tx.FailPendingInput(ctx); err != nil {
		return err
	}
	if err := CancelWork(ctx, tx); err != nil {
		return err
	}
	state, err := tx.LoadEnvironmentInput(ctx)
	if err != nil {
		return err
	}
	activity, pending := InputActivity(state)
	usage, err := tx.LoadUsage(ctx)
	if err != nil {
		return err
	}
	return tx.AppendChanges(ctx, environmentFailedChanges(EnvironmentFailure{Reason: reason, FailedAt: failedAt}, activity, pending, usage)...)
}

// EnvironmentExpiryTx is the Session transaction ExpireEnvironment runs in.
type EnvironmentExpiryTx interface {
	CancellationTx
	InputActivityTx
	// FailPendingInput settles the Session's pending Environment input
	// reservation as failed.
	FailPendingInput(ctx context.Context) error
	// ExpireEnvironment sets the status of the Session's Environment to
	// expired.
	ExpireEnvironment(ctx context.Context, environment string) error
}

// ExpireEnvironment ends the Session's Environment without a failure of its
// own: a live Environment expires, input that never ran fails, so the Session
// reads failed with its reason instead of idle, and active work is cancelled.
// It reports the input activity this changes.
func ExpireEnvironment(ctx context.Context, tx EnvironmentExpiryTx, environment Environment) error {
	return TrackInputActivity(ctx, tx, func(ctx context.Context) error {
		if environment.Status != "failed" && environment.Status != "expired" {
			if err := tx.ExpireEnvironment(ctx, environment.ID); err != nil {
				return err
			}
		}
		if err := tx.FailPendingInput(ctx); err != nil {
			return err
		}
		return CancelWork(ctx, tx)
	})
}

// EnvironmentTerminationTx is the Session transaction TerminateEnvironment
// runs in.
type EnvironmentTerminationTx interface {
	EnvironmentFailureTx
	// LoadEnvironment reads the Session's Environment.
	LoadEnvironment(ctx context.Context) (Environment, error)
	// ExpireEnvironment sets the status of the Session's Environment to
	// expired.
	ExpireEnvironment(ctx context.Context, environment string) error
}

// TerminateEnvironment settles the Session's Environment when its managed
// compute ends. A live Environment whose compute did not expire fails with the
// reason and detail, as FailEnvironment reports it; any other ends as
// ExpireEnvironment reports it.
func TerminateEnvironment(ctx context.Context, tx EnvironmentTerminationTx, computeExpired bool, reason string, detail *ProvisioningFailureDetail) error {
	environment, err := tx.LoadEnvironment(ctx)
	if err != nil {
		return err
	}
	if environment.Status != "failed" && environment.Status != "expired" && !computeExpired {
		if environment.Initialization == "complete" {
			// The category describes a failed initialization, which this was not.
			detail = nil
		}
		return FailEnvironment(ctx, tx, environment, reason, detail)
	}
	return ExpireEnvironment(ctx, tx, environment)
}
