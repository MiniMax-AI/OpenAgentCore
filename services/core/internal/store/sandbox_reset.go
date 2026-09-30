package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrSandboxResetAdmission = errors.New("hosted admission is paused for a sandbox reset")

// SandboxInUseError rejects reset completion while hosted resources remain.
type SandboxInUseError struct{ Resources deployment.Resources }

func (e *SandboxInUseError) Error() string {
	return "hosted sandbox resources still belong to this deployment"
}
func (e *SandboxInUseError) Unwrap() error { return deployment.ErrConflict }

type SandboxResetRequest struct {
	ExpectedGeneration uint64 `json:"expected_generation" binding:"required" minimum:"0"`
	Clear              string `json:"clear" binding:"required" enums:"auto,force"`
	DeadlineSeconds    *int32 `json:"deadline_seconds,omitempty" minimum:"300" maximum:"86400"`
}

func checkSandboxGeneration(d sqlc.RuntimeDeployment, installation string, generation uint64) error {
	if uint64(d.Generation) != generation {
		return &deployment.GenerationStaleError{CurrentGeneration: uint64(d.Generation)}
	}
	if !d.WebManaged || runtimeUUID(d.InstallationID) != installation {
		return deployment.ErrConflict
	}
	return nil
}

func (s *Store) resetTransaction(ctx context.Context, apply func(context.Context, *sqlc.Queries, sqlc.RuntimeDeployment) error) error {
	if err := s.checkExecutionAuthority(); err != nil {
		return err
	}
	return s.writer.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		d, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		return apply(ctx, q, d)
	})
}

// StartSandboxReset starts or escalates the reset. It returns no view: the
// caller reads the deployment after commit.
func (s *Store) StartSandboxReset(ctx context.Context, installation string, input SandboxResetRequest) error {
	if input.Clear != "auto" && input.Clear != "force" {
		return sessions.ErrInvalidInput
	}
	deadline := int32(3600)
	if input.DeadlineSeconds != nil {
		if input.Clear != "auto" || *input.DeadlineSeconds < 300 || *input.DeadlineSeconds > 86400 {
			return sessions.ErrInvalidInput
		}
		deadline = *input.DeadlineSeconds
	}
	return s.resetTransaction(ctx, func(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if err := checkSandboxGeneration(d, installation, input.ExpectedGeneration); err != nil {
			return err
		}
		if d.ProviderKind == "" {
			return deployment.ErrNotConfigured
		}
		if d.ResetClear.Valid {
			if d.ResetClear.String != input.Clear {
				if input.Clear != "force" {
					return deployment.ErrResetInProgress
				}
				if err := q.ForceSandboxReset(ctx); err != nil {
					return err
				}
				if err := auditpg.RecordDeploymentMutation(ctx, q, "reset_force", "sandbox_deployment", installation); err != nil {
					return err
				}
			}
		} else {
			source, ok := adminaudit.FromContext(ctx)
			if !ok {
				return sessions.ErrInvalidInput
			}
			// The audit insert validates the provenance before this transaction
			// can commit. Persist only the same non-secret typed source.
			audit, err := json.Marshal(source)
			if err != nil {
				return err
			}
			if err := q.StartSandboxReset(ctx, sqlc.StartSandboxResetParams{Clear: pgtype.Text{String: input.Clear, Valid: true}, DeadlineSeconds: deadline, Audit: audit}); err != nil {
				return err
			}
			return auditpg.RecordDeploymentMutation(ctx, q, "reset_start", "sandbox_deployment", installation)
		}
		return nil
	})
}

// CancelSandboxReset cancels a running reset. It returns no view: the caller
// reads the deployment after commit.
func (s *Store) CancelSandboxReset(ctx context.Context, installation string, generation uint64) error {
	return s.resetTransaction(ctx, func(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if err := checkSandboxGeneration(d, installation, generation); err != nil {
			return err
		}
		if d.ResetClear.Valid {
			if err := q.CancelSandboxReset(ctx); err != nil {
				return err
			}
			return auditpg.RecordDeploymentMutation(ctx, q, "reset_cancel", "sandbox_deployment", installation)
		}
		return nil
	})
}

// AdvanceSandboxResetDeadline makes force escalation durable before selecting
// work. A restart cannot extend an administrator's original absolute deadline.
func (s *Store) AdvanceSandboxResetDeadline(ctx context.Context) error {
	return s.resetTransaction(ctx, func(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if d.ResetClear.String != "auto" || !d.ResetDeadlineAt.Valid || time.Now().Before(d.ResetDeadlineAt.Time) {
			return nil
		}
		source, err := sandboxResetAudit(d)
		if err != nil {
			return err
		}
		if err := q.ForceSandboxReset(ctx); err != nil {
			return err
		}
		return auditpg.RecordDeploymentMutation(adminaudit.WithSource(ctx, source), q, "reset_deadline", "sandbox_deployment", runtimeUUID(d.InstallationID))
	})
}

func sandboxResetAudit(d sqlc.RuntimeDeployment) (adminaudit.Source, error) {
	var source adminaudit.Source
	if !d.ResetClear.Valid || json.Unmarshal(d.ResetAudit, &source) != nil {
		return source, sessions.ErrInvalidInput
	}
	return source, nil
}

// CompleteSandboxReset must run after the manager has drained. It does not take
// Session locks: archive and admission always lock Session before deployment.
// It returns the committed, unconfigured generation.
func (s *Store) CompleteSandboxReset(ctx context.Context, installation string, generation uint64, requestedAt time.Time) (uint64, error) {
	var committed uint64
	err := s.resetTransaction(ctx, func(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if err := checkSandboxGeneration(d, installation, generation); err != nil {
			return err
		}
		if !d.ResetClear.Valid || !d.ResetRequestedAt.Time.Equal(requestedAt) {
			return deployment.ErrConflict
		}
		if d.Generation == math.MaxInt64 || d.OwnerEpoch == math.MaxInt64 {
			return deployment.ErrConflict
		}
		resources, err := q.CountRuntimeDeploymentResources(ctx)
		if err != nil {
			return err
		}
		if resources.Allocations != 0 || resources.Pending != 0 {
			return &SandboxInUseError{deployment.Resources{Allocations: resources.Allocations, Pending: resources.Pending}}
		}
		source, err := sandboxResetAudit(d)
		if err != nil {
			return err
		}
		if err := q.CompleteSandboxReset(ctx); err != nil {
			return err
		}
		// The locked row advances by exactly one generation.
		committed = uint64(d.Generation) + 1
		if err := q.RetireSandboxNodes(ctx); err != nil {
			return err
		}
		if err := q.RetireSandboxEnrollments(ctx); err != nil {
			return err
		}
		if err := q.ClearSandboxGenerations(ctx); err != nil {
			return err
		}
		return auditpg.RecordDeploymentMutation(adminaudit.WithSource(ctx, source), q, "reset_complete", "sandbox_deployment", installation)
	})
	if err != nil {
		return 0, err
	}
	return committed, nil
}

type SandboxResetSession struct{ SessionID, TenantID string }

func (s *Store) ListSandboxResetSessions(ctx context.Context, after string, force bool) ([]SandboxResetSession, error) {
	cursor := pgtype.UUID{Valid: true}
	if after != "" {
		var err error
		cursor, err = parseID(after)
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.queries.ListSandboxResetSessions(ctx, sqlc.ListSandboxResetSessionsParams{AfterID: cursor, Force: force})
	if err != nil {
		return nil, err
	}
	result := make([]SandboxResetSession, 0, len(rows))
	for _, row := range rows {
		result = append(result, SandboxResetSession{SessionID: runtimeUUID(row.ID), TenantID: runtimeUUID(row.TenantID)})
	}
	return result, nil
}
