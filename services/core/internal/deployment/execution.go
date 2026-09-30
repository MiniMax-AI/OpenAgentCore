package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		if d.InstallationID != "" {
			if !d.WebManaged || d.InstallationID != id {
				return ErrConflict
			}
		} else {
			resources, err := tx.CountResources()
			if err != nil {
				return err
			}
			if resources.Allocations != 0 || resources.Pending != 0 {
				return ErrConflict
			}
		}
		if d.Provider != "" {
			if _, err := e.service.specification(d); err != nil {
				return err
			}
		}
		return tx.ClaimInstallation(id)
	})
}

// ConfigureProcess records the deployment process configuration selects,
// before the Worker starts. AdmissionPaused must be committed for the old
// installation before any switch. A nil selection never forgets the previous
// identity or unresolved resources.
func (e *ExecutionOperations) ConfigureProcess(ctx context.Context, selected *ProcessDeployment) error {
	var installation string
	if selected != nil {
		copy := *selected
		selected = &copy
		id, err := parseID(selected.InstallationID)
		if err != nil {
			return err
		}
		installation = id
		if !validDigest(selected.BackendFingerprint) {
			return fmt.Errorf("%w: invalid backend identity fingerprint", ErrInvalidInput)
		}
	}
	return e.storage.WithDeployment(ctx, func(tx DeploymentTx) error {
		previous, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if previous.WebManaged {
			return ErrConflict
		}
		if selected != nil && previous.InstallationID == installation && previous.BackendFingerprint == selected.BackendFingerprint && (previous.Provider == "" || selected.ProviderKind == previous.Provider) {
			if err := tx.SetProcessDeployment(installation, selected.BackendFingerprint, selected.AdmissionPaused); err != nil {
				return err
			}
			return e.configureManager(tx, previous, selected, installation)
		}
		resources, err := tx.CountResources()
		if err != nil {
			return err
		}
		if selected == nil {
			if previous.InstallationID != "" && (resources.Allocations != 0 || resources.Pending != 0) {
				return fmt.Errorf("cannot disable managed sandbox provider: %d unreleased allocations (instances, retained snapshots, uncertain operations or pending cleanup) and %d pending hosted environments remain", resources.Allocations, resources.Pending)
			}
			return nil
		}
		if previous.InstallationID == "" {
			if resources.Allocations != 0 {
				return fmt.Errorf("cannot adopt sandbox installation: %d existing unreleased allocations (including retained snapshots and pending cleanup) have no verified backend identity", resources.Allocations)
			}
		} else {
			if !previous.AdmissionPaused || !selected.AdmissionPaused {
				return fmt.Errorf("cannot switch sandbox installation: persist maintenance on the previous installation and keep the new installation in maintenance")
			}
			if resources.Allocations != 0 || resources.Pending != 0 {
				return fmt.Errorf("cannot switch sandbox installation: %d unreleased allocations (instances, retained snapshots, uncertain operations or pending cleanup) and %d pending hosted environments remain", resources.Allocations, resources.Pending)
			}
		}
		if err := tx.SetProcessDeployment(installation, selected.BackendFingerprint, selected.AdmissionPaused); err != nil {
			return err
		}
		return e.configureManager(tx, previous, selected, installation)
	})
}

// configureManager records the node provider and the local node process
// configuration selects.
func (e *ExecutionOperations) configureManager(tx DeploymentTx, previous Record, selected *ProcessDeployment, installation string) error {
	if selected.ProviderKind == "" {
		return nil
	}
	isNode, err := e.service.registry.IsNode(selected.ProviderKind)
	if err != nil {
		return err
	}
	if !isNode {
		return ErrInvalidInput
	}
	if previous.Provider == "" {
		resources, err := tx.CountResources()
		if err != nil {
			return err
		}
		if resources.Allocations != 0 || resources.Pending != 0 {
			return fmt.Errorf("cannot adopt historical sandbox resources: keep the original Core responsible for retained resources and install this release separately")
		}
	}
	var localNode string
	if selected.LocalNodeID != "" {
		id, err := parseID(selected.LocalNodeID)
		if err != nil {
			return err
		}
		localNode = id
		if previous.LocalNodeID != "" && previous.LocalNodeID != id {
			resources, err := tx.CountResources()
			if err != nil {
				return err
			}
			if resources.Allocations != 0 || resources.Pending != 0 || !previous.AdmissionPaused || !selected.AdmissionPaused {
				return fmt.Errorf("local sandbox node identity changed: restore its original state directory; replacement requires maintenance and no retained resources")
			}
		}
		if !validDigest(selected.LocalCredentialSHA256) {
			return ErrInvalidInput
		}
		if err := validateNode("Local", selected.LocalMaxActive, selected.LocalMaxRetained); err != nil {
			return err
		}
		n, err := tx.LoadNode(id)
		if errors.Is(err, ErrNotFound) {
			_, err = tx.InsertNode(NewNode{ID: id, InstallationID: installation, Name: "Local", BackendFingerprint: selected.BackendFingerprint, CredentialDigest: selected.LocalCredentialSHA256, MaxActive: selected.LocalMaxActive, MaxRetained: selected.LocalMaxRetained})
		} else if err == nil {
			if n.InstallationID != installation || n.BackendFingerprint != selected.BackendFingerprint || n.CredentialDigest != selected.LocalCredentialSHA256 {
				return fmt.Errorf("local sandbox node identity does not match the retained backend")
			}
			err = tx.UpdateNode(id, NodeLimits{Name: n.Name, MaxActive: selected.LocalMaxActive, MaxRetained: selected.LocalMaxRetained})
		}
		if err != nil {
			return err
		}
	}
	return tx.SetManagerDeployment(selected.ProviderKind, localNode)
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
		Generation: d.Generation + 1, IdleSeconds: description.IdleSeconds, RetentionSeconds: description.RetentionSeconds, Specification: specification,
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
