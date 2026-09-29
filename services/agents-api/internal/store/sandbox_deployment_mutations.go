package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/providers"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	var spec sandbox.DeploymentSpec
	if json.Unmarshal(d.Specification, &spec) != nil {
		return false, ErrSandboxDeploymentConflict
	}
	if spec.Digest(d.ProviderKind) != input.DeploymentSpec.Digest(input.Provider) {
		return false, nil
	}
	if d.ProviderKind != input.Provider {
		return false, nil
	}
	if input.E2B == nil {
		return d.E2bTemplate == "", nil
	}
	if d.E2bTemplate != input.E2B.Template {
		return false, nil
	}
	saved := input
	saved.E2B = &sandbox.E2BConfiguration{APIKey: input.E2B.APIKey, Template: d.E2bTemplate, APIURL: d.E2bApiUrl, Domain: d.E2bDomain}
	saved, savedErr := providers.Normalize(saved)
	normalized, inputErr := providers.Normalize(input)
	if savedErr != nil || inputErr != nil || saved.E2B.APIURL != normalized.E2B.APIURL || saved.E2B.Domain != normalized.E2B.Domain {
		return false, nil
	}
	key, err := s.credentialCipher.OpenSandboxDeployment(d.E2bCredential, runtimeUUID(d.InstallationID), uint64(d.Generation))
	if err != nil {
		return false, ErrSandboxCredentialUnavailable
	}
	return string(key) == input.E2B.APIKey, nil
}

func (s *Store) saveSandboxSelection(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment, input SandboxDeploymentSetupRequest) error {
	// Only a complete specification is stored, including derived E2B resources.
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
	normalized, err := providers.Normalize(input)
	if err != nil {
		return sandboxConfigurationError(err)
	}
	input = normalized
	params := sqlc.InitializeSandboxDeploymentParams{ProviderKind: input.Provider, BackendFingerprint: description.BackendFingerprint, Generation: generation, Mode: description.Mode, IdleSeconds: description.IdleSeconds, RetentionSeconds: description.RetentionSeconds}
	params.Specification, _ = json.Marshal(input.DeploymentSpec)
	if input.E2B != nil {
		encrypted, err := s.credentialCipher.SealSandboxDeployment([]byte(input.E2B.APIKey), runtimeUUID(d.InstallationID), uint64(generation))
		if err != nil {
			return ErrSandboxCredentialUnavailable
		}
		params.E2bCredential, params.E2bTemplate = encrypted, input.E2B.Template
		params.E2bApiUrl, params.E2bDomain = input.E2B.APIURL, input.E2B.Domain
		build := templateBuildColumns(input.E2B.TemplateBuild)
		params.E2bTemplateBuildStatus, params.E2bTemplateCpus = build.E2bTemplateBuildStatus, build.E2bTemplateCpus
		params.E2bTemplateMemoryMib, params.E2bTemplateRootDiskMib = build.E2bTemplateMemoryMib, build.E2bTemplateRootDiskMib
	}
	return q.InitializeSandboxDeployment(ctx, params)
}

func templateBuildColumns(build *sandbox.TemplateBuild) sqlc.RecordSandboxTemplateBuildParams {
	var params sqlc.RecordSandboxTemplateBuildParams
	if build != nil {
		params.E2bTemplateBuildStatus = pgtype.Text{String: build.Status, Valid: true}
		params.E2bTemplateCpus = pgtype.Int4{Int32: build.CPUs, Valid: true}
		params.E2bTemplateMemoryMib = pgtype.Int4{Int32: build.MemoryMiB, Valid: true}
		if build.RootDiskMiB != nil {
			params.E2bTemplateRootDiskMib = pgtype.Int4{Int32: *build.RootDiskMiB, Valid: true}
		}
	}
	return params
}

// recordTemplateBuild saves the build read by this request's validation when
// the selection is otherwise unchanged, without a new generation. Saving the
// same E2B selection again thus records a build that an older Core did not.
func recordTemplateBuild(ctx context.Context, q *sqlc.Queries, input SandboxDeploymentSetupRequest) error {
	if input.E2B == nil || input.E2B.TemplateBuild == nil {
		return nil
	}
	return q.RecordSandboxTemplateBuild(ctx, templateBuildColumns(input.E2B.TemplateBuild))
}

func (s *Store) InitializeSandboxDeployment(ctx context.Context, installationID string, input SandboxDeploymentSetupRequest) (RuntimeDeploymentView, error) {
	if s.executionLease == nil {
		return RuntimeDeploymentView{}, ErrInvalidInput
	}
	if err := validateSandboxSelection(input); err != nil {
		return RuntimeDeploymentView{}, err
	}
	id, err := parseConnectionGeneration(installationID)
	if err != nil {
		return RuntimeDeploymentView{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	defer cancel()
	var result RuntimeDeploymentView
	err = s.executionLease.transaction(ctx, func(tx pgx.Tx) error {
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
			if err := recordTemplateBuild(ctx, q, input); err != nil {
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
	if s.executionLease == nil {
		return ErrInvalidInput
	}
	if err := validateSandboxSelection(input.SandboxDeploymentSetupRequest); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	defer cancel()
	return s.executionLease.transaction(ctx, func(tx pgx.Tx) error {
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
	if s.executionLease == nil {
		return RuntimeDeploymentView{}, ErrInvalidInput
	}
	if err := validateSandboxSelection(input.SandboxDeploymentSetupRequest); err != nil {
		return RuntimeDeploymentView{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	defer cancel()
	var result RuntimeDeploymentView
	err := s.executionLease.transaction(ctx, func(tx pgx.Tx) error {
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
		if !equal || input.E2B != nil && input.E2B.ReplaceCredential {
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
				if input.E2B != nil && input.E2B.ReplaceCredential {
					action = "replace_credential"
				}
				if err := recordDeploymentMutation(ctx, q, action, "sandbox_deployment", installation); err != nil {
					return err
				}
			}
		} else if err := recordTemplateBuild(ctx, q, input.SandboxDeploymentSetupRequest); err != nil {
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
