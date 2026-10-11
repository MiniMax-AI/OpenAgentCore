package workspaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

// Controls constructs the registered adapter for a retained configuration.
type Controls interface {
	Control(workspacefs.ConfigurationRecord) (workspacefs.Control, error)
	Normalize(workspacefs.Configuration, json.RawMessage) (workspacefs.ConfigurationRecord, error)
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

// validate checks a retained record against its own configuration and admitted
// capacity, never today's selection or deployment request.
func validate(record Record, tenant, environment string, requirements *workspacefs.Requirements, control workspacefs.Control) error {
	if record.Reference.TenantID != tenant || record.Reference.EnvironmentID != environment {
		return workspacefs.ErrOwnership
	}
	if err := record.Reference.Validate(); err != nil {
		return err
	}
	if requirements == nil {
		return workspacefs.ErrUnsupported
	}
	return workspacefs.ValidateCombination(*requirements, control.Declaration(), record.CapacityMiB)
}

// Ensure persists the binding before creation and retries only filesystem Create,
// whose protocol requires convergence. Compute reservation follows this operation.
// capacity is the deployment's request; it is admitted only for a new binding.
func (e *ExecutionOperations) Ensure(ctx context.Context, tenant, environment string, requirements *workspacefs.Requirements, capacity uint32) (*workspacefs.Binding, error) {
	record, err := e.storage.Get(ctx, tenant, environment)
	switch {
	case err == nil:
		// Revalidate the retained binding under the Session lock.
		record, err = e.execution.Bind(ctx, tenant, environment, record.Configuration.ID, record.CapacityMiB)
	case errors.Is(err, ErrNotFound):
		record, err = e.admit(ctx, tenant, environment, requirements, capacity)
	}
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
	if err = validate(record, tenant, environment, requirements, control); err != nil {
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
	attachment, err := control.Create(ctx, record.Reference, record.CapacityMiB)
	if err != nil {
		return nil, err
	}
	if err = workspacefs.ValidateAttachment(record.Reference, record.Configuration.Configuration, attachment); err != nil {
		return nil, err
	}
	if err = e.execution.MarkReady(ctx, record.Reference, attachment); err != nil {
		return nil, err
	}
	record.Attachment = &attachment
	return readyBinding(record)
}

// admit validates the selected configuration against the request before any
// allocation; Bind then fences a concurrent selection change.
func (e *ExecutionOperations) admit(ctx context.Context, tenant, environment string, requirements *workspacefs.Requirements, capacity uint32) (Record, error) {
	config, err := e.storage.ActiveConfiguration(ctx)
	if err != nil {
		return Record{}, err
	}
	control, err := e.controls.Control(config)
	if err != nil {
		return Record{}, err
	}
	if requirements == nil {
		return Record{}, workspacefs.ErrUnsupported
	}
	if err = workspacefs.ValidateCombination(*requirements, control.Declaration(), capacity); err != nil {
		return Record{}, err
	}
	return e.execution.Bind(ctx, tenant, environment, config.ID, capacity)
}

// readyBinding projects only the public configuration; the credential stays in Core.
func readyBinding(record Record) (*workspacefs.Binding, error) {
	if record.Attachment == nil {
		return nil, workspacefs.ErrOwnership
	}
	if err := workspacefs.ValidateAttachment(record.Reference, record.Configuration.Configuration, *record.Attachment); err != nil {
		return nil, err
	}
	return &workspacefs.Binding{Configuration: record.Configuration.Configuration, Attachment: *record.Attachment}, nil
}

// GetReady never creates a filesystem or substitutes today's selected adapter.
func (e *ExecutionOperations) GetReady(ctx context.Context, tenant, environment string, requirements *workspacefs.Requirements) (*workspacefs.Binding, error) {
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
	if err = validate(record, tenant, environment, requirements, control); err != nil {
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

// Selection reads the single operator-owned selection and tenant capacity policy.
func (e *ExecutionOperations) Selection(ctx context.Context) (Selection, error) {
	return e.storage.Selection(ctx)
}

// Configure qualifies and publishes selection while the caller holds the shared
// deployment mutation gate. Existing bindings keep their immutable configuration
// and admitted capacity; the tenant policy applies to new bindings only.
func (e *ExecutionOperations) Configure(ctx context.Context, input SelectionInput, validate func(workspacefs.Declaration) error) (Selection, error) {
	if err := input.Configuration.Validate(); err != nil {
		return Selection{}, err
	}
	credential := input.Credential
	if credential == nil {
		// An encoded credential is valid credential input and encodes to itself.
		existing, err := e.storage.Configuration(ctx, input.ID)
		if err == nil {
			credential = existing.Credential
		} else if !errors.Is(err, ErrNotFound) {
			return Selection{}, err
		}
	}
	record, err := e.controls.Normalize(input.Configuration, credential)
	if err != nil {
		return Selection{}, err
	}
	control, err := e.controls.Control(record)
	if err != nil {
		return Selection{}, err
	}
	declaration := control.Declaration()
	if err = validate(declaration); err != nil {
		return Selection{}, err
	}
	if (input.TenantCapacityMiB > 0) != declaration.CapacityQuota {
		return Selection{}, fmt.Errorf("%w: tenant capacity requires, and is required by, a quota-enforcing configuration", workspacefs.ErrUnsupported)
	}
	if err = e.ownership.CheckOwnership(ctx); err != nil {
		return Selection{}, err
	}
	if err = control.Check(ctx); err != nil {
		return Selection{}, err
	}
	if err = e.ownership.CheckOwnership(ctx); err != nil {
		return Selection{}, err
	}
	if err = e.execution.SelectConfiguration(ctx, record, input.TenantCapacityMiB); err != nil {
		return Selection{}, err
	}
	return Selection{Configuration: record.Configuration, TenantCapacityMiB: input.TenantCapacityMiB}, nil
}
