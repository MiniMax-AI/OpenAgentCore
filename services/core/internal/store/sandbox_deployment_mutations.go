package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/jackc/pgx/v5"
)

var ErrSandboxCredentialUnavailable = errors.New("sandbox credential encryption is unavailable")

type SandboxDeploymentUpdateRequest struct {
	SandboxDeploymentSetupRequest
	ExpectedGeneration uint64 `json:"expected_generation"`
}

func validateSandboxSelection(input SandboxDeploymentSetupRequest) error {
	_, err := providers.Normalize(input)
	if err != nil {
		return sandboxConfigurationError(err)
	}
	return nil
}

func (s *Store) sandboxSelectionEqual(d sqlc.RuntimeDeployment, input SandboxDeploymentSetupRequest) (bool, error) {
	if d.ProviderKind != input.Provider {
		return false, nil
	}
	previous, err := s.sandboxSetup(d)
	if err != nil {
		return false, err
	}
	normalized, err := providers.Normalize(input)
	if err != nil {
		return false, sandboxConfigurationError(err)
	}
	if previous.Specification.Digest(d.ProviderKind) != normalized.DeploymentSpec.Digest(input.Provider) {
		return false, nil
	}
	return providers.Equal(input.Provider, previous.Configuration, normalized.Configuration)
}

func configurationJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
func (s *Store) saveSandboxSelection(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment, input SandboxDeploymentSetupRequest) error {
	if err := providers.ValidateSpecification(input.Provider, input.DeploymentSpec); err != nil {
		return sandboxConfigurationError(err)
	}
	if d.Generation == math.MaxInt64 {
		return ErrSandboxDeploymentConflict
	}
	generation := d.Generation + 1
	description, err := providers.Describe(input.Provider, runtimeUUID(d.InstallationID))
	if err != nil {
		return sandboxConfigurationError(err)
	}
	input, err = providers.Normalize(input)
	if err != nil {
		return sandboxConfigurationError(err)
	}
	record, err := providers.Encode(input.Provider, input.Configuration)
	if err != nil {
		return sandboxConfigurationError(err)
	}
	params := sqlc.InitializeSandboxDeploymentParams{ProviderKind: input.Provider, BackendFingerprint: description.BackendFingerprint, Generation: generation, Mode: description.Mode, IdleSeconds: description.IdleSeconds, RetentionSeconds: description.RetentionSeconds, ProviderConfig: configurationJSON(record.Public), ProviderMetadata: configurationJSON(record.Metadata)}
	params.Specification, _ = json.Marshal(input.DeploymentSpec)
	if len(record.Secret) > 0 {
		params.ProviderCredential, err = s.credentialCipher.SealSandboxDeployment(record.Secret, runtimeUUID(d.InstallationID), uint64(generation))
		if err != nil {
			return ErrSandboxCredentialUnavailable
		}
	}
	return q.InitializeSandboxDeployment(ctx, params)
}

func recordConfigurationMetadata(ctx context.Context, q *sqlc.Queries, input SandboxDeploymentSetupRequest) error {
	record, err := providers.Encode(input.Provider, input.Configuration)
	if err != nil {
		return sandboxConfigurationError(err)
	}
	if len(record.Metadata) == 0 {
		return nil
	}
	return q.RecordSandboxConfigurationMetadata(ctx, record.Metadata)
}

func (s *Store) InitializeSandboxDeployment(ctx context.Context, installationID string, input SandboxDeploymentSetupRequest) (RuntimeDeploymentView, error) {
	if err := s.checkExecutionAuthority(); err != nil {
		return RuntimeDeploymentView{}, err
	}
	if err := validateSandboxSelection(input); err != nil {
		return RuntimeDeploymentView{}, err
	}
	id, err := parseConnectionGeneration(installationID)
	if err != nil {
		return RuntimeDeploymentView{}, err
	}
	var result RuntimeDeploymentView
	err = s.writer.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		d, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		if err := checkSandboxGeneration(d, installationID, input.ExpectedGeneration); err != nil {
			return err
		}
		if d.ResetClear.Valid {
			return ErrSandboxResetInProgress
		}
		if d.InstallationID != id {
			return ErrSandboxDeploymentConflict
		}
		if d.ProviderKind != "" {
			equal, err := s.sandboxSelectionEqual(d, input)
			if err != nil {
				return err
			}
			if !equal {
				return ErrSandboxDeploymentConflict
			}
			if err := recordConfigurationMetadata(ctx, q, input); err != nil {
				return err
			}
		} else if err := s.saveSandboxSelection(ctx, q, d, input); err != nil {
			return err
		}
		result, err = s.deploymentView(ctx, q)
		return err
	})
	return result, err
}

// CheckSandboxDeploymentSwitch is a preliminary check only. The mutation repeats
// it in the committing transaction; no database lock spans provider work.
func (s *Store) CheckSandboxDeploymentSwitch(ctx context.Context, installation string, input SandboxDeploymentUpdateRequest) error {
	if err := s.checkExecutionAuthority(); err != nil {
		return err
	}
	if err := validateSandboxSelection(input.SandboxDeploymentSetupRequest); err != nil {
		return err
	}
	return s.writer.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		d, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		return checkSandboxSwitch(ctx, q, d, installation, input)
	})
}
func checkSandboxSwitch(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment, installation string, input SandboxDeploymentUpdateRequest) error {
	if err := checkSandboxGeneration(d, installation, input.ExpectedGeneration); err != nil {
		return err
	}
	if d.ResetClear.Valid {
		return ErrSandboxResetInProgress
	}
	if d.ProviderKind == "" {
		return ErrSandboxNotConfigured
	}
	if _, err := deploymentSpecification(d); err != nil {
		return err
	}
	if d.ProviderKind != input.Provider {
		return &SandboxResetRequiredError{CurrentProvider: d.ProviderKind, RequestedProvider: input.Provider}
	}

	return nil
}

func (s *Store) UpdateSandboxDeployment(ctx context.Context, installation string, input SandboxDeploymentUpdateRequest) (RuntimeDeploymentView, error) {
	if err := s.checkExecutionAuthority(); err != nil {
		return RuntimeDeploymentView{}, err
	}
	if err := validateSandboxSelection(input.SandboxDeploymentSetupRequest); err != nil {
		return RuntimeDeploymentView{}, err
	}
	var result RuntimeDeploymentView
	err := s.writer.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		d, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		if err := checkSandboxSwitch(ctx, q, d, installation, input); err != nil {
			return err
		}
		equal, err := s.sandboxSelectionEqual(d, input.SandboxDeploymentSetupRequest)
		if err != nil {
			return err
		}
		if !equal || input.ReplacesCredential() {
			if err := q.RetainSandboxGeneration(ctx); err != nil {
				return err
			}
			if err := s.saveSandboxSelection(ctx, q, d, input.SandboxDeploymentSetupRequest); err != nil {
				return err
			}
			if err := q.CollectSandboxGenerations(ctx); err != nil {
				return err
			}
			{
				action := "change"
				if input.ReplacesCredential() {
					action = "replace_credential"
				}
				if err := recordDeploymentMutation(ctx, q, action, "sandbox_deployment", installation); err != nil {
					return err
				}
			}
		} else if err := recordConfigurationMetadata(ctx, q, input.SandboxDeploymentSetupRequest); err != nil {
			return err
		}
		result, err = s.deploymentView(ctx, q)
		return err
	})
	return result, err
}

// CheckSandboxDeploymentSetup rejects stale/reset state before provider preparation;
// InitializeSandboxDeployment repeats the check in its committing transaction.
func (s *Store) CheckSandboxDeploymentSetup(ctx context.Context, installation string, input SandboxDeploymentSetupRequest) error {
	return s.resetTransaction(ctx, func(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if err := checkSandboxGeneration(d, installation, input.ExpectedGeneration); err != nil {
			return err
		}
		if d.ResetClear.Valid {
			return ErrSandboxResetInProgress
		}
		if err := validateSandboxSelection(input); err != nil {
			return err
		}
		if d.ProviderKind != "" {
			if _, err := deploymentSpecification(d); err != nil {
				return err
			}
		}
		if d.ProviderKind != "" && d.ProviderKind != input.Provider {
			return &SandboxResetRequiredError{CurrentProvider: d.ProviderKind, RequestedProvider: input.Provider}
		}
		return nil
	})
}
