package sessions

import (
	"context"
	"encoding/json"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// EnvironmentInputState is the Session's latest Environment input reservation
// that no Turn has admitted or superseded, with the facts of its Environment.
// While a Turn is active or newer than the latest reservation, the Session has
// none.
type EnvironmentInputState struct {
	// State is the reservation's state.
	State   string
	Initial bool
	// CreatedAt is when the reservation was made, and SettledAt when it stopped
	// being pending; SettledAt is zero while it is pending.
	CreatedAt time.Time
	SettledAt time.Time
	// FailureCode is the recorded failure of a failed reservation, empty when
	// none was recorded.
	FailureCode   string
	EnvironmentID string
	// EnvironmentType is the type the Session's Environment configuration names.
	EnvironmentType string
	// EnvironmentStatus is the Environment's connection status.
	EnvironmentStatus string
}

// InputActivity projects the Session's input activity from its latest
// reservation, nil when it has none, and reports whether that reservation is
// still pending and can start a Turn. A hosted initial input that is pending or
// cancelled has no activity: no Turn has started, and the pinned Session
// contract permits idle while a hosted Environment provisions, so neither a
// caller action nor an invented transition is reported. It still reports
// whether the input is pending.
func InputActivity(state *EnvironmentInputState) (*EnvironmentInputActivity, bool) {
	if state == nil {
		return nil, false
	}
	pending := state.State == EnvironmentInputPending
	activity := &EnvironmentInputActivity{Status: "idle", LastActiveAt: state.CreatedAt}
	if !state.SettledAt.IsZero() {
		activity.LastActiveAt = state.SettledAt
	}
	if state.State == EnvironmentInputFailed {
		activity.Status, activity.Failure = "failed", "environment_unavailable"
		if state.FailureCode != "" {
			activity.Failure = state.FailureCode
		}
	}
	if state.Initial && state.State == EnvironmentInputExpired {
		activity.Status = "failed"
	}
	if state.EnvironmentType == "openai_hosted" && state.Initial &&
		(state.State == EnvironmentInputPending || state.State == EnvironmentInputCancelled) {
		return nil, pending
	}
	if pending && state.EnvironmentType != "openai_hosted" && state.EnvironmentStatus != "connected" {
		activity.Status = "requires_action"
		activity.EnvironmentID = state.EnvironmentID
	}
	return activity, pending
}

// inputActivityChanged reports whether after is input activity the Session has
// not reported yet: it exists and differs from before in status, Environment or
// failure. Admitted input has no activity; its Turn and Session events report
// it.
func inputActivityChanged(before, after *EnvironmentInputActivity) bool {
	if after == nil {
		return false
	}
	return before == nil || before.Status != after.Status || before.EnvironmentID != after.EnvironmentID || before.Failure != after.Failure
}

// inputActivityChange is the agent.session.<status> snapshot that reports input
// activity with the Session's usage. It is settled once the reservation stops
// being pending.
func inputActivityChange(activity *EnvironmentInputActivity, pending bool, usage json.RawMessage) SessionChange {
	return SessionChange{
		Event:                    v1.SessionEvent{Type: "agent.session." + activity.Status},
		EnvironmentInputActivity: activity, SessionUsage: usage, Settled: !pending,
	}
}

// InputActivityTx is the Session transaction TrackInputActivity runs in.
type InputActivityTx interface {
	JournalTx
	// LoadEnvironmentInput reads the Session's latest Environment input
	// reservation that no Turn has admitted or superseded, nil when there is
	// none.
	LoadEnvironmentInput(ctx context.Context) (*EnvironmentInputState, error)
}

// TrackInputActivity runs apply, the caller's writes in the same transaction,
// and reports the input activity they change. It reads the activity before and
// after apply, and only when it changed reads the Session's usage and journals
// the snapshot. When apply fails, it returns at once and the caller's
// transaction rolls back.
func TrackInputActivity(ctx context.Context, tx InputActivityTx, apply func(context.Context) error) error {
	state, err := tx.LoadEnvironmentInput(ctx)
	if err != nil {
		return err
	}
	before, _ := InputActivity(state)
	if err := apply(ctx); err != nil {
		return err
	}
	if state, err = tx.LoadEnvironmentInput(ctx); err != nil {
		return err
	}
	after, pending := InputActivity(state)
	if !inputActivityChanged(before, after) {
		return nil
	}
	usage, err := tx.LoadUsage(ctx)
	if err != nil {
		return err
	}
	return tx.AppendChanges(ctx, inputActivityChange(after, pending, usage))
}
