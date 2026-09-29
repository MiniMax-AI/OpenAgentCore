package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrSandboxCredentialUnavailable = errors.New("sandbox credential encryption is unavailable")

// APIKey is internal configuration. HTTP requests use a write-only DTO.
// TemplateBuild is set only by Core after it validates the candidate.
type SandboxE2BConfiguration struct {
	ReplaceCredential bool                     `json:"-"`
	APIKey            string                   `json:"-"`
	APIURL            string                   `json:"api_url,omitempty"`
	Domain            string                   `json:"domain,omitempty"`
	Template          string                   `json:"template"`
	TemplateBuild     *SandboxE2BTemplateBuild `json:"-"`
}

// SandboxE2BTemplateBuild is the fixed build as read by the validation that
// admitted a selection. RootDiskMiB is nil when E2B does not report it.
type SandboxE2BTemplateBuild struct {
	Status          string
	CPUs, MemoryMiB int32
	RootDiskMiB     *int32
}
type SandboxDeploymentUpdateRequest struct {
	SandboxDeploymentSetupRequest
	ExpectedGeneration uint64 `json:"expected_generation"`
}

// E2BResourcesPending reports an E2B selection that omitted resources. Core
// fills them from the validated template build before any write.
func E2BResourcesPending(input SandboxDeploymentSetupRequest) bool {
	return input.Provider == "e2b" && input.Resources == (sandbox.Resources{})
}

func validateSandboxSelection(input SandboxDeploymentSetupRequest) error {
	if E2BResourcesPending(input) {
		if input.Runtime != nil {
			return sandboxConfigurationError(&sandbox.ValidationError{Param: "runtime", Message: "invalid sandbox configuration: E2B Runtime is selected by its immutable template build"})
		}
	} else if err := input.DeploymentSpec.Validate(input.Provider); err != nil {
		return sandboxConfigurationError(err)
	}
	switch input.Provider {
	case "docker", "microsandbox":
		if input.E2B != nil {
			return ErrInvalidInput
		}
	case "e2b":
		if input.E2B == nil || input.E2B.APIKey == "" || len(input.E2B.APIKey) > 4096 || strings.IndexFunc(input.E2B.APIKey, func(r rune) bool { return unicode.IsSpace(r) || r == 0 }) >= 0 {
			return ErrInvalidInput
		}
		if _, _, err := e2b.NormalizeEndpoint(input.E2B.APIURL, input.E2B.Domain); err != nil {
			return ErrInvalidInput
		}
		template, build, ok := strings.Cut(input.E2B.Template, ":")
		id, err := uuid.Parse(build)
		if !ok || template == "" || len(template) > 128 || err != nil || id == uuid.Nil || id.String() != build {
			return ErrInvalidInput
		}
		for _, c := range template {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return ErrInvalidInput
			}
		}
	default:
		return ErrInvalidInput
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
	savedAPI, savedDomain, savedErr := e2b.NormalizeEndpoint(d.E2bApiUrl, d.E2bDomain)
	inputAPI, inputDomain, inputErr := e2b.NormalizeEndpoint(input.E2B.APIURL, input.E2B.Domain)
	if savedErr != nil || inputErr != nil || savedAPI != inputAPI || savedDomain != inputDomain {
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
	if err := input.DeploymentSpec.Validate(input.Provider); err != nil {
		return sandboxConfigurationError(err)
	}
	if d.Generation == math.MaxInt64 {
		return ErrSandboxDeploymentConflict
	}
	generation := d.Generation + 1
	mode := "nodes"
	namespace := "nodes:" + runtimeUUID(d.InstallationID)
	if input.Provider == "e2b" {
		mode = "direct"
		namespace = "e2b:" + runtimeUUID(d.InstallationID)
	}
	digest := sha256.Sum256([]byte(input.Provider + "\x00" + namespace))
	params := sqlc.InitializeSandboxDeploymentParams{ProviderKind: input.Provider, BackendFingerprint: hex.EncodeToString(digest[:]), Generation: generation, Mode: mode}
	params.Specification, _ = json.Marshal(input.DeploymentSpec)
	if input.Provider == "microsandbox" {
		params.IdleSeconds, params.RetentionSeconds = 300, 86400
	}
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

func templateBuildColumns(build *SandboxE2BTemplateBuild) sqlc.RecordSandboxTemplateBuildParams {
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
