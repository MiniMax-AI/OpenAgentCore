package deploymentpg

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
)

// healthRecord is a node's stored health, with its last host observation.
type healthRecord struct {
	deployment.NodeHealth
	Host *deployment.NodeHost `json:"host,omitempty"`
}

// parseID returns the stored form of an identifier, and ErrInvalidInput for a
// value that is not a nonzero UUID.
func parseID(value string) (pgtype.UUID, error) {
	id, err := pgunit.ParseID(value)
	if errors.Is(err, pgunit.ErrInvalidID) {
		return id, deployment.ErrInvalidInput
	}
	return id, err
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func timestamp(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

// translate replaces PostgreSQL's rejection of unstorable client text.
func translate(err error) error {
	if pgunit.IsUnstorableText(err) {
		return textvalue.ErrUnstorable
	}
	return err
}

// record converts the stored deployment. open opens a stored credential; a
// view never needs it. Without a key the credential error is
// credentialcrypto.ErrUnavailable; a credential the key cannot open or
// authenticate is an internal decryption error.
func record(d sqlc.RuntimeDeployment, cipher *credentialcrypto.Cipher, open bool) deployment.Record {
	r := deployment.Record{InstallationID: uuidString(d.InstallationID), WebManaged: d.WebManaged, Provider: d.ProviderKind, BackendFingerprint: d.BackendFingerprint,
		Generation: uint64(d.Generation), OwnerEpoch: uint64(d.OwnerEpoch), Mode: d.Mode, AdmissionPaused: d.AdmissionPaused, LocalNodeID: uuidString(d.LocalNodeID),
		IdleSeconds: d.IdleSeconds, RetentionSeconds: d.RetentionSeconds, Specification: d.Specification,
		Configuration: sandbox.ConfigurationRecord{Public: d.ProviderConfig, Metadata: d.ProviderMetadata}, CredentialStored: len(d.ProviderCredential) > 0}
	if r.CredentialStored && open {
		if cipher == nil {
			r.CredentialError = credentialcrypto.ErrUnavailable
		} else if secret, err := cipher.OpenSandboxDeployment(d.ProviderCredential, r.InstallationID, r.Generation); err != nil {
			r.CredentialError = errors.New("sandbox deployment credential decryption failed")
		} else {
			r.Configuration.Secret = secret
		}
	}
	if d.ResetClear.Valid {
		r.Reset = &deployment.ResetState{Clear: d.ResetClear.String, RequestedAt: d.ResetRequestedAt.Time, DeadlineAt: timestamp(d.ResetDeadlineAt), ForcedAt: timestamp(d.ResetForcedAt)}
	}
	return r
}

func snapshot(row sqlc.GetSandboxDeploymentSnapshotRow) (deployment.Snapshot, error) {
	result := deployment.Snapshot{Record: record(row.RuntimeDeployment, nil, false), Resources: deployment.Resources{Allocations: row.Allocations, Pending: row.Pending}}
	if err := json.Unmarshal(row.Rollout, &result.Rollout); err != nil {
		return deployment.Snapshot{}, err
	}
	if result.Record.Reset != nil {
		if err := json.Unmarshal(row.Remaining, &result.Remaining); err != nil {
			return deployment.Snapshot{}, err
		}
	}
	return result, nil
}

func generation(g sqlc.RuntimeDeploymentGeneration) deployment.GenerationRecord {
	return deployment.GenerationRecord{Generation: uint64(g.Generation), Provider: g.ProviderKind, Specification: g.Specification, Configuration: sandbox.ConfigurationRecord{Public: g.ProviderConfig, Metadata: g.ProviderMetadata}}
}

func storedNode(n sqlc.RuntimeNode) deployment.StoredNode {
	result := deployment.StoredNode{ID: uuidString(n.ID), InstallationID: uuidString(n.InstallationID), Name: n.Name, BackendFingerprint: n.BackendFingerprint, CredentialDigest: n.CredentialSha256,
		MaxActive: int(n.MaxActive), MaxRetained: int(n.MaxRetained), ConnectionID: uuidString(n.ConnectionID), ConnectedEpoch: uint64(n.ConnectedEpoch),
		SpecificationDigest: n.SpecificationDigest, DeploymentGeneration: uint64(n.DeploymentGeneration)}
	if n.ReadyGeneration.Valid {
		ready := uint64(n.ReadyGeneration.Int64)
		result.ReadyGeneration = &ready
	}
	return result
}

func nodeRecords(rows []sqlc.ListRuntimeNodesRow) ([]deployment.NodeRecord, error) {
	out := make([]deployment.NodeRecord, 0, len(rows))
	for _, n := range rows {
		var health healthRecord
		if err := json.Unmarshal(n.Health, &health); err != nil {
			return nil, err
		}
		health.NodeHealth.Host = health.Host
		record := deployment.NodeRecord{ID: uuidString(n.ID), Name: n.Name, Provider: n.ProviderKind, CoreURL: n.CoreUrl, CreatedAt: n.CreatedAt.Time, LastSeenAt: timestamp(n.LastSeenAt),
			Health: health.NodeHealth, Online: n.Online, ServingReady: n.ServingReady, ProtocolVersion: int(n.ProtocolVersion), DeploymentGeneration: uint64(n.DeploymentGeneration),
			TargetGeneration: uint64(n.TargetGeneration), TargetState: n.TargetState, TargetDiagnostic: n.TargetDiagnostic, MaxActive: int(n.MaxActive), MaxRetained: int(n.MaxRetained),
			Active: n.Active, Retained: n.Retained, Reserved: n.Reserved, CleanupPending: n.CleanupPending, Running: n.Running, Snapshots: n.Snapshots}
		if n.EnrollmentID.Valid {
			id := uuidString(n.EnrollmentID)
			record.EnrollmentID = &id
		}
		if n.ReadyGeneration.Valid {
			ready := uint64(n.ReadyGeneration.Int64)
			record.ReadyGeneration = &ready
		}
		out = append(out, record)
	}
	return out, nil
}
