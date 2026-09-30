package deployment

import (
	"context"
	"math"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
)

// ResetRequest starts or escalates a reset of the sandbox deployment.
type ResetRequest struct {
	ExpectedGeneration uint64 `json:"expected_generation" binding:"required" minimum:"0"`
	Clear              string `json:"clear" binding:"required" enums:"auto,force"`
	DeadlineSeconds    *int32 `json:"deadline_seconds,omitempty" minimum:"300" maximum:"86400"`
}

// ResetSession is a hosted Session a reset still has to archive.
type ResetSession struct{ SessionID, TenantID string }

const (
	// ResetAuto archives idle Sessions and escalates to ResetForce at the
	// deadline.
	ResetAuto = "auto"
	// ResetForce archives every hosted Session, busy ones included.
	ResetForce = "force"

	defaultResetDeadlineSeconds = 3600
)

// MinResetDeadlineSeconds and MaxResetDeadlineSeconds bound an auto reset's
// deadline.
const (
	MinResetDeadlineSeconds = 300
	MaxResetDeadlineSeconds = 86400
)

// resetDeadline validates the request's clear mode and returns the auto
// deadline in seconds. Only auto takes a deadline.
func resetDeadline(input ResetRequest) (int32, error) {
	if input.Clear != ResetAuto && input.Clear != ResetForce {
		return 0, ErrInvalidInput
	}
	if input.DeadlineSeconds == nil {
		return defaultResetDeadlineSeconds, nil
	}
	if input.Clear != ResetAuto || *input.DeadlineSeconds < MinResetDeadlineSeconds || *input.DeadlineSeconds > MaxResetDeadlineSeconds {
		return 0, ErrInvalidInput
	}
	return *input.DeadlineSeconds, nil
}

// StartReset pauses admission and starts the reset, or escalates a running
// auto reset to force. Repeating the running mode is a no-op, and auto never
// downgrades force. The context must carry the administrator audit source,
// which the reset keeps for the audit entries it records later. It returns no
// view: the caller reads the deployment after commit.
func (e *ExecutionOperations) StartReset(ctx context.Context, installation string, input ResetRequest) error {
	deadline, err := resetDeadline(input)
	if err != nil {
		return err
	}
	return e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if err := checkGeneration(d, installation, input.ExpectedGeneration); err != nil {
			return err
		}
		if d.Provider == "" {
			return ErrNotConfigured
		}
		if d.Reset != nil {
			if d.Reset.Clear == input.Clear {
				return nil
			}
			if input.Clear != ResetForce {
				return ErrResetInProgress
			}
			if err := tx.ForceReset(); err != nil {
				return err
			}
			return tx.RecordAudit("reset_force", installation)
		}
		source, ok := adminaudit.FromContext(ctx)
		if !ok {
			return ErrInvalidInput
		}
		if err := tx.StartReset(input.Clear, deadline, source); err != nil {
			return err
		}
		return tx.RecordAudit("reset_start", installation)
	})
}

// CancelReset cancels a running reset and restores admission. Sessions it
// already archived stay archived. Without a running reset it only checks the
// generation. It returns no view: the caller reads the deployment after commit.
func (e *ExecutionOperations) CancelReset(ctx context.Context, installation string, generation uint64) error {
	return e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if err := checkGeneration(d, installation, generation); err != nil {
			return err
		}
		if d.Reset == nil {
			return nil
		}
		if err := tx.CancelReset(); err != nil {
			return err
		}
		return tx.RecordAudit("reset_cancel", installation)
	})
}

// AdvanceResetDeadline escalates an auto reset to force once its deadline has
// passed, before the reset selects work. The deadline is absolute, so a
// restart never extends it. The audit entry carries the administrator source
// that started the reset.
func (e *ExecutionOperations) AdvanceResetDeadline(ctx context.Context) error {
	return e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if d.Reset == nil || d.Reset.Clear != ResetAuto || d.Reset.DeadlineAt == nil || time.Now().Before(*d.Reset.DeadlineAt) {
			return nil
		}
		source, err := tx.LoadResetSource()
		if err != nil {
			return err
		}
		if err := tx.ForceReset(); err != nil {
			return err
		}
		return tx.RecordAuditAs(source, "reset_deadline", d.InstallationID)
	})
}

// CompleteReset clears the drained deployment: it forgets the selection,
// retires the nodes, enrollments and retained generations, and advances the
// generation and owner epoch by one. It runs after the manager has drained
// and takes no Session lock, because archive and admission lock the Session
// before the deployment. It returns the committed, unconfigured generation,
// and InUseError while hosted resources remain.
func (e *ExecutionOperations) CompleteReset(ctx context.Context, installation string, generation uint64, requestedAt time.Time) (uint64, error) {
	var committed uint64
	err := e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if err := checkGeneration(d, installation, generation); err != nil {
			return err
		}
		if d.Reset == nil || !d.Reset.RequestedAt.Equal(requestedAt) {
			return ErrConflict
		}
		if d.Generation >= math.MaxInt64 || d.OwnerEpoch >= math.MaxInt64 {
			return ErrConflict
		}
		resources, err := tx.CountResources()
		if err != nil {
			return err
		}
		if resources.Allocations != 0 || resources.Pending != 0 {
			return &InUseError{Resources: resources}
		}
		source, err := tx.LoadResetSource()
		if err != nil {
			return err
		}
		if err := tx.CompleteReset(); err != nil {
			return err
		}
		committed = d.Generation + 1
		return tx.RecordAuditAs(source, "reset_complete", installation)
	})
	if err != nil {
		return 0, err
	}
	return committed, nil
}
