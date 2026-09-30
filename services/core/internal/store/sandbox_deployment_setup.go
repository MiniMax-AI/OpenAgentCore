package store

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/jackc/pgx/v5"
)

var ErrSandboxDeploymentConflict = errors.New("sandbox deployment is already configured differently")

type SandboxDeploymentSetupRequest = sandbox.Selection

// SandboxSetup is the immutable configuration selected by the deployment admin.
// An empty Provider means that Web setup has not yet selected an adapter.
type SandboxSetup struct {
	Specification                                sandbox.DeploymentSpec
	InstallationID, Provider, BackendFingerprint string
	Generation                                   uint64
	Mode                                         string
	AdmissionPaused                              bool
	Configuration                                sandbox.Configuration `json:"-"`
	IdleSeconds, RetentionSeconds                int64
}

func (s *Store) GetSandboxSetup(ctx context.Context) (SandboxSetup, error) {
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	d, err := s.queries.GetRuntimeDeployment(ctx)
	if err != nil {
		return SandboxSetup{}, err
	}
	return s.sandboxSetup(d)
}

func (s *Store) sandboxSetup(d sqlc.RuntimeDeployment) (SandboxSetup, error) {
	if !d.WebManaged {
		return SandboxSetup{}, ErrSandboxDeploymentConflict
	}
	result := SandboxSetup{InstallationID: runtimeUUID(d.InstallationID), Provider: d.ProviderKind, BackendFingerprint: d.BackendFingerprint, IdleSeconds: d.IdleSeconds, RetentionSeconds: d.RetentionSeconds, Generation: uint64(d.Generation), Mode: d.Mode, AdmissionPaused: d.AdmissionPaused}
	if err := json.Unmarshal(d.Specification, &result.Specification); err != nil {
		return SandboxSetup{}, err
	}
	if d.ProviderKind != "" {
		if err := providers.ValidateSpecification(d.ProviderKind, result.Specification); err != nil {
			return SandboxSetup{}, err
		}
	}
	if d.ProviderKind != "" {
		var secret []byte
		if len(d.ProviderCredential) > 0 {
			var err error
			secret, err = s.credentialCipher.OpenSandboxDeployment(d.ProviderCredential, result.InstallationID, result.Generation)
			if err != nil {
				return SandboxSetup{}, ErrSandboxCredentialUnavailable
			}
		}
		var err error
		result.Configuration, err = providers.Decode(d.ProviderKind, sandbox.ConfigurationRecord{Public: d.ProviderConfig, Metadata: d.ProviderMetadata, Secret: secret})
		if err != nil {
			return SandboxSetup{}, ErrSandboxDeploymentConflict
		}
	}

	return result, nil
}

// ClaimWebSandboxDeployment runs exactly once per execution-owner startup. It
// reserves the installation before selection and fences previous node presence.
func (s *Store) ClaimWebSandboxDeployment(ctx context.Context, installationID string) error {
	if err := s.checkExecutionAuthority(); err != nil {
		return err
	}
	id, err := parseConnectionGeneration(installationID)
	if err != nil {
		return ErrInvalidInput
	}
	return s.writer.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
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
		if d.ProviderKind != "" {
			if _, err := deploymentSpecification(d); err != nil {
				return err
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

func runtimeDeploymentView(d sqlc.RuntimeDeployment, publicURL string) (RuntimeDeploymentView, error) {
	result := RuntimeDeploymentView{InstallationID: runtimeUUID(d.InstallationID), Provider: d.ProviderKind, CoreURL: publicURL, OwnerEpoch: uint64(d.OwnerEpoch), Generation: uint64(d.Generation), Mode: d.Mode}
	if len(d.Specification) > 0 && string(d.Specification) != "{}" {
		var spec sandbox.DeploymentSpec
		if json.Unmarshal(d.Specification, &spec) == nil {
			result.Specification = &spec
			result.SpecificationDigest = spec.Digest(d.ProviderKind)
		}
	}
	if d.ProviderKind != "" {
		value, err := providers.Decode(d.ProviderKind, sandbox.ConfigurationRecord{Public: d.ProviderConfig, Metadata: d.ProviderMetadata})
		if err != nil {
			return RuntimeDeploymentView{}, ErrSandboxDeploymentConflict
		}
		record, err := providers.Encode(d.ProviderKind, value)
		if err != nil {
			return RuntimeDeploymentView{}, ErrSandboxDeploymentConflict
		}
		result.Configuration = configurationJSON(record.Public)
		result.Metadata = configurationJSON(record.Metadata)
		result.CredentialConfigured = len(d.ProviderCredential) > 0
	}

	if providers.SupportsCheckpoint(d.ProviderKind) {
		result.Suspension = &SandboxSuspensionView{IdleSeconds: d.IdleSeconds, RetentionSeconds: d.RetentionSeconds}
	}
	return result, nil
}
