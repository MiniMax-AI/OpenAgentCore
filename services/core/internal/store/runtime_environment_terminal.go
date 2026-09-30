package store

import (
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5/pgtype"
)

func environmentFailure(row sqlc.Environment) *sessions.EnvironmentFailure {
	if row.Status != "failed" || !row.FailureReason.Valid || !row.FailedAt.Valid {
		return nil
	}
	failure := &sessions.EnvironmentFailure{Reason: row.FailureReason.String, FailedAt: row.FailedAt.Time}
	var detail sessions.ProvisioningFailureDetail
	if json.Unmarshal(row.FailureDetail, &detail) == nil {
		failure.Detail = sessions.SanitizedProvisioningDetail(detail)
	}
	return failure
}

// terminateRuntimeEnvironment participates in the allocation's Session transaction.
// Public expiry does not assert compute removal or invent an expired SSE variant.
// A first failure records the hosted provisioning failure; an already terminal
// Environment only settles remaining input, without repeating events.
func terminateRuntimeEnvironment(ctx context.Context, q *sqlc.Queries, current sqlc.GetRuntimeAllocationRow, reason string, detail *sessions.ProvisioningFailureDetail, cancel func() error) error {
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
func failEnvironment(ctx context.Context, q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow, session pgtype.UUID, reason string, detail *sessions.ProvisioningFailureDetail, cancel func() error) error {
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
	usage, err := sessionpg.LoadUsage(ctx, q, session)
	if err != nil {
		return err
	}
	if err := sessionpg.AppendChanges(ctx, q, session, sessions.SessionChange{Event: v1.SessionEvent{
		Type: "error", Error: &v1.StreamError{Type: "environment_error", Code: "sandbox_error", Message: reason},
	}}); err != nil {
		return err
	}
	return sessionpg.AppendChanges(ctx, q, session, sessions.SessionChange{
		Event:                    v1.SessionEvent{Type: "agent.session.failed"},
		EnvironmentInputActivity: activity, EnvironmentFailure: &sessions.EnvironmentFailure{Reason: reason, FailedAt: failedAt.Time},
		SessionUsage: usage, Settled: !pending,
	})
}
