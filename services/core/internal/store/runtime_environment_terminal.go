package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// provisioningFailureReason is the safe reason for an Environment that
// failed without a confirmed failed step: timeouts, unknown effects, missing or
// old receipts, bootstrap rejection and Core restart during initialization.
const provisioningFailureReason = "Failed to provision environment: initialization did not complete"

// Provisioning step kinds for ProvisioningFailure.Step.
const (
	ProvisioningSetupCommand   = "setup"
	ProvisioningPythonPackages = "python"
	ProvisioningNPMPackages    = "npm"
	ProvisioningInitialFile    = "file"
	ProvisioningSkill          = "skill"
	ProvisioningHarness        = "harness"
)

// ProvisioningFailure identifies a confirmed failed initialization step.
// It cannot carry Runtime output: Step selects a fixed label, Index is the setup
// command position and ExitCode is the Runtime-reported status (0 when absent).
type ProvisioningFailure struct {
	Step     string
	Index    int
	ExitCode int
}

// reason renders the public Session error. The setup_commands and Python package
// labels match observed official errors (which append raw pip output for Python;
// Core never does). The npm, file and Skill labels are unverified.
// A script step without a reported exit status keeps the generic reason.
func (f ProvisioningFailure) reason() string {
	label := map[string]string{
		ProvisioningPythonPackages: "Python package installation",
		ProvisioningNPMPackages:    "npm package installation",
	}[f.Step]
	if f.Step == ProvisioningSetupCommand && f.Index >= 0 {
		label = fmt.Sprintf("setup_commands[%d]", f.Index)
	}
	switch {
	case label != "" && f.ExitCode > 0 && f.ExitCode < 256:
		return fmt.Sprintf("Failed to provision environment: script %q failed with exit code %d", label, f.ExitCode)
	case f.Step == ProvisioningInitialFile:
		return "Failed to provision environment: initial file installation failed"
	case f.Step == ProvisioningHarness:
		return "Failed to prepare environment: the selected Harness is unavailable. Install the supported Harness version on the Runtime and create a new Session."
	case f.Step == ProvisioningSkill:
		return "Failed to provision environment: Skill installation failed"
	}
	return provisioningFailureReason
}

// EnvironmentFailure is an Environment's recorded provisioning failure. It
// makes the Session failed with this reason and last activity time.
type EnvironmentFailure struct {
	Reason   string                     `json:"reason"`
	FailedAt time.Time                  `json:"failed_at"`
	Detail   *ProvisioningFailureDetail `json:"-"`
}

func environmentFailure(row sqlc.Environment) *EnvironmentFailure {
	if row.Status != "failed" || !row.FailureReason.Valid || !row.FailedAt.Valid {
		return nil
	}
	failure := &EnvironmentFailure{Reason: row.FailureReason.String, FailedAt: row.FailedAt.Time}
	var detail ProvisioningFailureDetail
	if json.Unmarshal(row.FailureDetail, &detail) == nil {
		failure.Detail = detail.sanitized()
	}
	return failure
}

// terminateRuntimeEnvironment participates in the allocation's Session transaction.
// Public expiry does not assert compute removal or invent an expired SSE variant.
// A first failure records the hosted provisioning failure; an already terminal
// Environment only settles remaining input, without repeating events.
func terminateRuntimeEnvironment(ctx context.Context, q *sqlc.Queries, current sqlc.GetRuntimeAllocationRow, reason string, detail *ProvisioningFailureDetail, cancel func() error) error {
	row, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: current.TenantID, ID: current.SessionID})
	if err != nil {
		return err
	}
	terminal := row.Environment.Status == "expired" || row.Environment.Status == "failed"
	if !terminal && !current.Expired {
		return failEnvironment(ctx, q, row, current.SessionID, reason, detail, cancel)
	}
	return withEnvironmentInputActivity(ctx, q, current.SessionID, func() error {
		if !terminal {
			if err := q.SetEnvironmentConnectionStatus(ctx, sqlc.SetEnvironmentConnectionStatusParams{ID: row.Environment.ID, Status: "expired"}); err != nil {
				return err
			}
		}
		if err := q.FailSessionEnvironmentInput(ctx, current.SessionID); err != nil {
			return err
		}
		return cancel()
	})
}

// failEnvironment records, in the caller's transaction and in the observed
// official order, agent.session.environment.failed, an error event carrying the
// safe reason, then one agent.session.failed snapshot. The snapshot captures the
// settled input activity, Usage and the failure, matching later Session reads.
// Pending input settles as failed exactly as before.
func failEnvironment(ctx context.Context, q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow, session pgtype.UUID, reason string, detail *ProvisioningFailureDetail, cancel func() error) error {
	var rawDetail []byte
	if detail != nil {
		var err error
		rawDetail, err = json.Marshal(detail)
		if err != nil {
			return err
		}
	}
	failedAt, err := q.RecordEnvironmentFailure(ctx, sqlc.RecordEnvironmentFailureParams{ID: row.Environment.ID, FailureReason: pgtype.Text{String: reason, Valid: true}, FailureDetail: rawDetail})
	if err != nil {
		return err
	}
	if err := recordEnvironmentState(ctx, q, row, "failed"); err != nil {
		return err
	}
	if err := q.FailSessionEnvironmentInput(ctx, session); err != nil {
		return err
	}
	if err := cancel(); err != nil {
		return err
	}
	activity, pending, err := environmentInputState(ctx, q, session)
	if err != nil {
		return err
	}
	usage, err := q.SessionTokenUsage(ctx, session)
	if err != nil {
		return err
	}
	if err := recordSessionChange(ctx, q, session, SessionChange{Event: v1.SessionEvent{
		Type: "error", Error: &v1.StreamError{Type: "environment_error", Code: "sandbox_error", Message: reason},
	}}); err != nil {
		return err
	}
	return recordSessionChange(ctx, q, session, SessionChange{
		Event:                    v1.SessionEvent{Type: "agent.session.failed"},
		EnvironmentInputActivity: activity, EnvironmentFailure: &EnvironmentFailure{Reason: reason, FailedAt: failedAt.Time},
		SessionUsage: usage, Settled: !pending,
	})
}
