package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"unicode"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrSandboxCredentialUnavailable = errors.New("sandbox credential encryption is unavailable")

// APIKey is internal configuration. HTTP requests use a write-only DTO.
type SandboxE2BConfiguration struct {
	APIKey   string `json:"-"`
	Template string `json:"template"`
}
type SandboxDeploymentUpdateRequest struct {
	SandboxDeploymentSetupRequest
	ExpectedGeneration uint64 `json:"expected_generation"`
}
type SandboxMaintenanceRequest struct {
	Maintenance        bool   `json:"maintenance"`
	ExpectedGeneration uint64 `json:"expected_generation"`
}

func validateSandboxSelection(input SandboxDeploymentSetupRequest) error {
	if ValidateSandboxCoreURL(input.CoreURL) != nil {
		return ErrInvalidInput
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
	if d.ProviderKind != input.Provider || d.CoreUrl != input.CoreURL {
		return false, nil
	}
	if input.E2B == nil {
		return d.E2bTemplate == "", nil
	}
	if d.E2bTemplate != input.E2B.Template {
		return false, nil
	}
	key, err := s.credentialCipher.OpenSandboxDeployment(d.E2bCredential, runtimeUUID(d.InstallationID), uint64(d.Generation))
	if err != nil {
		return false, ErrSandboxCredentialUnavailable
	}
	return string(key) == input.E2B.APIKey, nil
}

func (s *Store) saveSandboxSelection(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment, input SandboxDeploymentSetupRequest) error {
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
	params := sqlc.InitializeSandboxDeploymentParams{ProviderKind: input.Provider, CoreUrl: input.CoreURL, BackendFingerprint: hex.EncodeToString(digest[:]), Generation: generation, Mode: mode}
	if input.Provider == "microsandbox" {
		params.IdleSeconds, params.RetentionSeconds = 300, 86400
	}
	if input.E2B != nil {
		encrypted, err := s.credentialCipher.SealSandboxDeployment([]byte(input.E2B.APIKey), runtimeUUID(d.InstallationID), uint64(generation))
		if err != nil {
			return ErrSandboxCredentialUnavailable
		}
		params.E2bCredential, params.E2bTemplate = encrypted, input.E2B.Template
	}
	return q.InitializeSandboxDeployment(ctx, params)
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
		if !d.WebManaged || d.InstallationID != id {
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
		} else if err := s.saveSandboxSelection(ctx, q, d, input); err != nil {
			return err
		}
		result, err = getRuntimeDeploymentView(ctx, q)
		return err
	})
	return result, err
}

// CheckSandboxDeploymentSwitch is a preliminary check only. The mutation repeats
// it after execution has drained; no database lock spans provider work.
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
	if !d.WebManaged || runtimeUUID(d.InstallationID) != installation || d.ProviderKind == "" || !d.Maintenance || uint64(d.Generation) != input.ExpectedGeneration || d.CoreUrl != input.CoreURL {
		return ErrSandboxDeploymentConflict
	}
	resources, err := q.CountRuntimeDeploymentResources(ctx)
	if err != nil {
		return err
	}
	if resources.Allocations != 0 || resources.Pending != 0 {
		return ErrSandboxDeploymentConflict
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
		if !equal {
			if err := s.saveSandboxSelection(ctx, q, d, input.SandboxDeploymentSetupRequest); err != nil {
				return err
			}
			if err := q.RetireSandboxNodes(ctx); err != nil {
				return err
			}
			if err := q.RetireSandboxEnrollments(ctx); err != nil {
				return err
			}
			if err := q.AdvanceSandboxOwnerEpoch(ctx); err != nil {
				return err
			}
		}
		result, err = getRuntimeDeploymentView(ctx, q)
		return err
	})
	return result, err
}

// The Worker verifies activation before calling this with maintenance=false.
func (s *Store) SetSandboxMaintenance(ctx context.Context, installation string, input SandboxMaintenanceRequest) (RuntimeDeploymentView, error) {
	if s.executionLease == nil {
		return RuntimeDeploymentView{}, ErrInvalidInput
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
		if !d.WebManaged || runtimeUUID(d.InstallationID) != installation || d.ProviderKind == "" || uint64(d.Generation) != input.ExpectedGeneration {
			return ErrSandboxDeploymentConflict
		}
		if err := q.SetSandboxMaintenance(ctx, input.Maintenance); err != nil {
			return err
		}
		result, err = getRuntimeDeploymentView(ctx, q)
		return err
	})
	return result, err
}
