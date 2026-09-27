package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrSandboxDeploymentConflict = errors.New("sandbox deployment is already configured differently")

type SandboxDeploymentSetupRequest struct {
	sandbox.DeploymentSpec
	Provider string                   `json:"provider"`
	E2B      *SandboxE2BConfiguration `json:"e2b,omitempty"`
}

// SandboxSetup is the immutable configuration selected by the deployment admin.
// An empty Provider means that Web setup has not yet selected an adapter.
type SandboxSetup struct {
	Specification                                sandbox.DeploymentSpec
	InstallationID, Provider, BackendFingerprint string
	Generation                                   uint64
	Mode                                         string
	Maintenance                                  bool
	E2B                                          *SandboxE2BConfiguration
	IdleSeconds, RetentionSeconds                int64
}

func (s *Store) GetSandboxSetup(ctx context.Context) (SandboxSetup, error) {
	ctx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	defer cancel()
	d, err := s.queries.GetRuntimeDeployment(ctx)
	if err != nil {
		return SandboxSetup{}, err
	}
	if !d.WebManaged {
		return SandboxSetup{}, ErrSandboxDeploymentConflict
	}
	result := SandboxSetup{InstallationID: runtimeUUID(d.InstallationID), Provider: d.ProviderKind, BackendFingerprint: d.BackendFingerprint, IdleSeconds: d.IdleSeconds, RetentionSeconds: d.RetentionSeconds, Generation: uint64(d.Generation), Mode: d.Mode, Maintenance: d.Maintenance}
	if err := json.Unmarshal(d.Specification, &result.Specification); err != nil {
		return SandboxSetup{}, err
	}
	if d.ProviderKind != "" && !unspecifiedNodeDeployment(d) {
		if err := result.Specification.Validate(d.ProviderKind); err != nil {
			return SandboxSetup{}, err
		}
	}
	if d.ProviderKind == "e2b" {
		credential, err := s.credentialCipher.OpenSandboxDeployment(d.E2bCredential, result.InstallationID, result.Generation)
		if err != nil {
			return SandboxSetup{}, ErrSandboxCredentialUnavailable
		}
		result.E2B = &SandboxE2BConfiguration{APIKey: string(credential), Template: d.E2bTemplate}
	}
	return result, nil
}

// ClaimWebSandboxDeployment runs exactly once per execution-owner startup. It
// reserves the installation before selection and fences previous node presence.
func (s *Store) ClaimWebSandboxDeployment(ctx context.Context, installationID string) error {
	id, err := parseConnectionGeneration(installationID)
	if err != nil || s.executionLease == nil {
		return ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	defer cancel()
	return s.executionLease.transaction(ctx, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		d, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		if d.InstallationID.Valid {
			if !d.WebManaged || d.InstallationID != id {
				return ErrSandboxDeploymentConflict
			}
		} else {
			resources, err := q.CountRuntimeDeploymentResources(ctx)
			if err != nil {
				return err
			}
			if resources.Allocations != 0 || resources.Pending != 0 {
				return ErrSandboxDeploymentConflict
			}
		}
		return q.ClaimWebSandboxDeployment(ctx, id)
	})
}

// ValidateSandboxCoreURL accepts a canonical public origin, never a path or
// credential. Plain HTTP is reserved for explicit loopback development hosts.
// OAC_PUBLIC_URL must pass it.
func ValidateSandboxCoreURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.String() != value || u.Host != strings.ToLower(u.Host) {
		return ErrInvalidInput
	}
	if strings.ContainsAny(u.Host, "\\% \t\r\n") || strings.HasSuffix(u.Host, ":") {
		return ErrInvalidInput
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return ErrInvalidInput
		}
	}
	loopback := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	} else {
		if len(u.Hostname()) > 253 || strings.ContainsAny(u.Host, "[]") {
			return ErrInvalidInput
		}
		for _, label := range strings.Split(u.Hostname(), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return ErrInvalidInput
			}
			for _, char := range label {
				if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
					return ErrInvalidInput
				}
			}
		}
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return ErrInvalidInput
	}
	return nil
}

// LoopbackOrigin reports whether a validated origin names a loopback host, which
// nothing outside the Core host can reach.
func LoopbackOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return ip.IsLoopback()
	}
	return u.Hostname() == "localhost"
}

func runtimeDeploymentView(d sqlc.RuntimeDeployment, publicURL string) RuntimeDeploymentView {
	result := RuntimeDeploymentView{InstallationID: runtimeUUID(d.InstallationID), Provider: d.ProviderKind, CoreURL: publicURL, Maintenance: d.Maintenance, OwnerEpoch: uint64(d.OwnerEpoch), Generation: uint64(d.Generation), Mode: d.Mode}
	if len(d.Specification) > 0 && string(d.Specification) != "{}" {
		var spec sandbox.DeploymentSpec
		if json.Unmarshal(d.Specification, &spec) == nil {
			result.Specification = &spec
			result.SpecificationDigest = spec.Digest(d.ProviderKind)
		}
	}
	if d.ProviderKind == "e2b" {
		result.E2B = &SandboxE2BView{Template: d.E2bTemplate, CredentialConfigured: len(d.E2bCredential) > 0,
			TemplateBuild: SandboxE2BTemplateBuildView{Resources: SandboxTemplateResources{
				CPUs: optionalInt32(d.E2bTemplateCpus), MemoryMiB: optionalInt32(d.E2bTemplateMemoryMib), RootDiskMiB: optionalInt32(d.E2bTemplateRootDiskMib)}}}
		if d.E2bTemplateBuildStatus.Valid {
			status := d.E2bTemplateBuildStatus.String
			result.E2B.TemplateBuild.Status = &status
		}
	}
	if d.ProviderKind == "microsandbox" {
		result.Suspension = &SandboxSuspensionView{IdleSeconds: d.IdleSeconds, RetentionSeconds: d.RetentionSeconds}
	}
	return result
}

func optionalInt32(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	return &value.Int32
}
