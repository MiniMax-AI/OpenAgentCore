package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// ExecutionOperations are the deployment changes only the execution owner
// makes. Every change runs in a transaction on the connection that holds the
// execution lease and repeats its checks there; provider work runs between
// transactions, never inside one.
type ExecutionOperations struct {
	service *Service
	storage ExecutionStorage
}

// NewExecutionOperations binds the deployment changes to the lease-bound
// storage of one execution owner.
func NewExecutionOperations(service *Service, storage ExecutionStorage) (*ExecutionOperations, error) {
	if service == nil || storage == nil {
		return nil, errors.New("deployment execution operations require the deployment service and execution storage")
	}
	return &ExecutionOperations{service: service, storage: storage}, nil
}

// Claim reserves the installation for Web setup, once per execution owner
// startup, and fences the previous owner epoch's node presence.
func (e *ExecutionOperations) Claim(ctx context.Context, installationID string) error {
	id, err := parseID(installationID)
	if err != nil {
		return err
	}
	return e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if d.InstallationID != "" && d.InstallationID != id {
			return ErrConflict
		}
		if d.Provider != "" {
			if _, err := e.service.specification(d); err != nil {
				return err
			}
		}
		return tx.ClaimInstallation(id)
	})
}

// CheckSetup rejects a stale or reset deployment before provider preparation.
// Initialize repeats the check in its committing transaction.
func (e *ExecutionOperations) CheckSetup(ctx context.Context, installation string, input sandbox.Selection) error {
	return e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if err := checkGeneration(d, installation, input.ExpectedGeneration); err != nil {
			return err
		}
		if d.Reset != nil {
			return ErrResetInProgress
		}
		if err := e.service.validateSelection(input); err != nil {
			return err
		}
		if d.Provider != "" {
			if _, err := e.service.specification(d); err != nil {
				return err
			}
			if d.Provider != input.Provider {
				return &ResetRequiredError{CurrentProvider: d.Provider, RequestedProvider: input.Provider}
			}
		}
		return nil
	})
}

// Initialize stores the first selection of the installation. The same
// selection at the current generation is a no-op; a different one conflicts.
func (e *ExecutionOperations) Initialize(ctx context.Context, installation string, input sandbox.Selection) (View, error) {
	if err := e.service.validateSelection(input); err != nil {
		return View{}, err
	}
	id, err := parseID(installation)
	if err != nil {
		return View{}, err
	}
	var result View
	err = e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if err := checkGeneration(d, installation, input.ExpectedGeneration); err != nil {
			return err
		}
		if d.Reset != nil {
			return ErrResetInProgress
		}
		if d.InstallationID != id {
			return ErrConflict
		}
		if d.Provider != "" {
			equal, err := e.service.selectionEqual(d, input)
			if err != nil {
				return err
			}
			if !equal {
				return ErrConflict
			}
			if err := e.recordMetadata(tx, input); err != nil {
				return err
			}
		} else if err := e.saveSelection(tx, d, input); err != nil {
			return err
		}
		result, err = e.committedView(tx)
		return err
	})
	if err != nil {
		return View{}, err
	}
	return result, nil
}

// ClassifyChange resolves an omitted credential before validation and reports
// whether the change leaves the deployment unchanged. An explicitly submitted
// credential is a verified change even when its bytes are unchanged.
func (e *ExecutionOperations) ClassifyChange(ctx context.Context, installation string, input sandbox.Selection) (sandbox.Selection, bool, error) {
	var unchanged bool
	err := e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if err := e.checkSwitch(d, installation, input); err != nil {
			return err
		}
		previous, err := e.service.setup(d)
		if err != nil {
			return err
		}
		resolved, err := e.service.registry.ResolveChange(input, previous.selection())
		if err != nil {
			return configurationError(err)
		}
		resolved.ExpectedGeneration = input.ExpectedGeneration
		input = resolved
		if err := e.service.validateSelection(input); err != nil {
			return err
		}
		equal, err := e.service.selectionEqual(d, input)
		unchanged = equal && !input.ReplacesCredential()
		return err
	})
	return input, unchanged, err
}

// checkSwitch rejects a change of a deployment that is stale, resetting,
// unconfigured or on another backend.
func (e *ExecutionOperations) checkSwitch(d Record, installation string, input sandbox.Selection) error {
	if err := checkGeneration(d, installation, input.ExpectedGeneration); err != nil {
		return err
	}
	if d.Reset != nil {
		return ErrResetInProgress
	}
	if d.Provider == "" {
		return ErrNotConfigured
	}
	if _, err := e.service.specification(d); err != nil {
		return err
	}
	if d.Provider != input.Provider {
		return &ResetRequiredError{CurrentProvider: d.Provider, RequestedProvider: input.Provider}
	}
	return nil
}

// Update changes the selection on the same backend. A different selection or a
// submitted credential stores the next generation and retains the previous one
// for the allocations that still use it.
func (e *ExecutionOperations) Update(ctx context.Context, installation string, input sandbox.Selection) (View, error) {
	if err := e.service.validateSelection(input); err != nil {
		return View{}, err
	}
	var result View
	err := e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if err := e.checkSwitch(d, installation, input); err != nil {
			return err
		}
		equal, err := e.service.selectionEqual(d, input)
		if err != nil {
			return err
		}
		if !equal || input.ReplacesCredential() {
			if err := tx.RetainGeneration(); err != nil {
				return err
			}
			if err := e.saveSelection(tx, d, input); err != nil {
				return err
			}
			if err := tx.CollectGenerations(); err != nil {
				return err
			}
			action := "change"
			if input.ReplacesCredential() {
				action = "replace_credential"
			}
			if err := tx.RecordAudit(action, installation); err != nil {
				return err
			}
		} else if err := e.recordMetadata(tx, input); err != nil {
			return err
		}
		result, err = e.committedView(tx)
		return err
	})
	if err != nil {
		return View{}, err
	}
	return result, nil
}

// CollectGenerations deletes retained generations nothing uses, serialized
// with admission and deployment changes.
func (e *ExecutionOperations) CollectGenerations(ctx context.Context) error {
	return e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		return tx.CollectGenerations()
	})
}

func (e *ExecutionOperations) committedView(tx DeploymentTx) (View, error) {
	snapshot, err := tx.LoadSnapshot()
	if err != nil {
		return View{}, err
	}
	return e.service.view(snapshot)
}

// saveSelection stores input as the deployment's next generation.
func (e *ExecutionOperations) saveSelection(tx DeploymentTx, d Record, input sandbox.Selection) error {
	registry := e.service.registry
	if err := registry.ValidateSpecification(input.Provider, input.DeploymentSpec); err != nil {
		return configurationError(err)
	}
	if d.Generation >= math.MaxInt64 {
		return ErrConflict
	}
	description, err := registry.Describe(input.Provider, d.InstallationID)
	if err != nil {
		return configurationError(err)
	}
	input, err = registry.Normalize(input)
	if err != nil {
		return configurationError(err)
	}
	record, err := registry.Encode(input.Provider, input.Configuration)
	if err != nil {
		return configurationError(err)
	}
	specification, err := json.Marshal(input.DeploymentSpec)
	if err != nil {
		return err
	}
	return tx.SaveSelection(SelectionRecord{InstallationID: d.InstallationID, Provider: input.Provider, BackendFingerprint: description.BackendFingerprint, Mode: description.Mode,
		Generation: d.Generation + 1, Specification: specification,
		Configuration: sandbox.ConfigurationRecord{Public: configurationJSON(record.Public), Metadata: configurationJSON(record.Metadata), Secret: record.Secret}})
}

// recordMetadata keeps the provider's latest non-secret metadata for an
// unchanged selection.
func (e *ExecutionOperations) recordMetadata(tx DeploymentTx, input sandbox.Selection) error {
	record, err := e.service.registry.Encode(input.Provider, input.Configuration)
	if err != nil {
		return configurationError(err)
	}
	if len(record.Metadata) == 0 {
		return nil
	}
	return tx.RecordConfigurationMetadata(record.Metadata)
}
