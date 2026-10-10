package workspaces

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

// Controls constructs the registered adapter for a retained configuration.
type Controls interface {
	Control(workspacefs.Configuration) (workspacefs.Control, error)
	Normalize(workspacefs.Configuration) (workspacefs.Configuration, error)
}
type Ownership interface{ CheckOwnership(context.Context) error }

// ExecutionOperations coordinates filesystem mutations under the execution lease.
type ExecutionOperations struct {
	storage   Storage
	execution ExecutionStorage
	controls  Controls
	ownership Ownership
}

func NewExecution(storage Storage, execution ExecutionStorage, controls Controls, ownership Ownership) *ExecutionOperations {
	return &ExecutionOperations{storage: storage, execution: execution, controls: controls, ownership: ownership}
}

func (e *ExecutionOperations) Declaration(ctx context.Context) (*workspacefs.Declaration, error) {
	c, err := e.storage.ActiveConfiguration(ctx)
	if errors.Is(err, ErrNotConfigured) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	control, err := e.controls.Control(c)
	if err != nil {
		return nil, err
	}
	d := control.Declaration()
	return &d, nil
}

func validate(record Record, tenant, environment string, requirements *workspacefs.Requirements, capacity uint32, control workspacefs.Control) error {
	if record.Reference.TenantID != tenant || record.Reference.EnvironmentID != environment {
		return workspacefs.ErrOwnership
	}
	if err := record.Reference.Validate(); err != nil {
		return err
	}
	if requirements == nil {
		return workspacefs.ErrUnsupported
	}
	return workspacefs.ValidateCombination(*requirements, control.Declaration(), capacity)
}

// Ensure persists the binding before creation and retries only filesystem Create,
// whose protocol requires convergence. Compute reservation follows this operation.
func (e *ExecutionOperations) Ensure(ctx context.Context, tenant, environment string, requirements *workspacefs.Requirements, capacity uint32) (*workspacefs.Binding, error) {
	record, err := e.execution.Bind(ctx, tenant, environment)
	if errors.Is(err, ErrNotConfigured) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	control, err := e.controls.Control(record.Configuration)
	if err != nil {
		return nil, err
	}
	if err = validate(record, tenant, environment, requirements, capacity, control); err != nil {
		return nil, err
	}
	if record.State == Ready {
		return readyBinding(record)
	}
	if record.State != Creating {
		return nil, ErrConflict
	}
	if err = e.ownership.CheckOwnership(ctx); err != nil {
		return nil, err
	}
	attachment, err := control.Create(ctx, record.Reference)
	if err != nil {
		return nil, err
	}
	if err = workspacefs.ValidateAttachment(record.Reference, record.Configuration, attachment); err != nil {
		return nil, err
	}
	if err = e.execution.MarkReady(ctx, record.Reference, attachment); err != nil {
		return nil, err
	}
	record.Attachment = &attachment
	return readyBinding(record)
}

func readyBinding(record Record) (*workspacefs.Binding, error) {
	if record.Attachment == nil {
		return nil, workspacefs.ErrOwnership
	}
	if err := workspacefs.ValidateAttachment(record.Reference, record.Configuration, *record.Attachment); err != nil {
		return nil, err
	}
	return &workspacefs.Binding{Configuration: record.Configuration, Attachment: *record.Attachment}, nil
}

// GetReady never creates a filesystem or substitutes today's selected adapter.
func (e *ExecutionOperations) GetReady(ctx context.Context, tenant, environment string, requirements *workspacefs.Requirements, capacity uint32) (*workspacefs.Binding, error) {
	record, err := e.storage.Get(ctx, tenant, environment)
	if errors.Is(err, ErrNotFound) {
		// Existing compute without a retained binding owns its original workspace,
		// even when the operator subsequently selects external storage.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record.State != Ready {
		return nil, ErrConflict
	}
	control, err := e.controls.Control(record.Configuration)
	if err != nil {
		return nil, err
	}
	if err = validate(record, tenant, environment, requirements, capacity, control); err != nil {
		return nil, err
	}
	return readyBinding(record)
}

// DeleteBatch scans explicit deletion intent independently of compute inventory.
// The store excludes every unreleased allocation and retains terminal identities.
func (e *ExecutionOperations) DeleteBatch(ctx context.Context, cursor string) (string, error) {
	if err := e.ownership.CheckOwnership(ctx); err != nil {
		return cursor, err
	}
	rows, err := e.storage.DeletionCandidates(ctx, cursor)
	if err != nil {
		return cursor, err
	}
	if len(rows) == 0 {
		return "", nil
	}
	for _, record := range rows {
		if ctx.Err() != nil {
			return cursor, ctx.Err()
		}
		operation, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = e.delete(operation, record)
		cancel()
		if err != nil {
			if ownership := e.ownership.CheckOwnership(ctx); ownership != nil {
				return cursor, ownership
			}
			log.Ctx(ctx).Warn("workspace deletion incomplete", "environment_id", record.Reference.EnvironmentID)
		}
		cursor = record.Reference.ObjectID
	}
	return cursor, nil
}

func (e *ExecutionOperations) delete(ctx context.Context, record Record) error {
	if record.State == Deleted {
		return nil
	}
	if record.State != Deleting {
		if err := e.execution.BeginDelete(ctx, record.Reference); err != nil {
			return err
		}
	}
	control, err := e.controls.Control(record.Configuration)
	if err != nil {
		return err
	}
	if err = e.ownership.CheckOwnership(ctx); err != nil {
		return err
	}
	if err = control.Delete(ctx, record.Reference); err != nil {
		return err
	}
	return e.execution.MarkDeleted(ctx, record.Reference)
}

// Configuration reads the single operator-owned selection.
func (e *ExecutionOperations) Configuration(ctx context.Context) (workspacefs.Configuration, error) {
	return e.storage.ActiveConfiguration(ctx)
}

// Configure qualifies and publishes selection while the caller holds the shared
// deployment mutation gate. Existing bindings keep their immutable configuration.
func (e *ExecutionOperations) Configure(ctx context.Context, configuration workspacefs.Configuration, validate func(workspacefs.Declaration) error) (workspacefs.Configuration, error) {
	canonical, err := e.controls.Normalize(configuration)
	if err != nil {
		return workspacefs.Configuration{}, err
	}
	control, err := e.controls.Control(canonical)
	if err != nil {
		return workspacefs.Configuration{}, err
	}
	if err = validate(control.Declaration()); err != nil {
		return workspacefs.Configuration{}, err
	}
	if err = e.ownership.CheckOwnership(ctx); err != nil {
		return workspacefs.Configuration{}, err
	}
	if err = control.Check(ctx); err != nil {
		return workspacefs.Configuration{}, err
	}
	if err = e.ownership.CheckOwnership(ctx); err != nil {
		return workspacefs.Configuration{}, err
	}
	if err = e.execution.SelectConfiguration(ctx, canonical); err != nil {
		return workspacefs.Configuration{}, err
	}
	return canonical, nil
}
