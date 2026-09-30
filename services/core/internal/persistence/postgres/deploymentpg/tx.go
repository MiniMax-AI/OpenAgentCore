package deploymentpg

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
)

// unit is one transaction or snapshot. locked reports that the transaction
// began by locking the deployment, so reads of it keep the lock.
type unit struct {
	ctx    context.Context
	q      *sqlc.Queries
	locked bool
}

var _ deployment.NodeReads = unit{}

// LoadDeployment reads the deployment without opening its credential: node
// transactions and snapshots never need it.
func (u unit) LoadDeployment() (deployment.Record, error) {
	d, err := u.loadDeployment()
	if err != nil {
		return deployment.Record{}, err
	}
	return record(d, nil, false), nil
}

func (u unit) loadDeployment() (sqlc.RuntimeDeployment, error) {
	if u.locked {
		return u.q.LockRuntimeDeployment(u.ctx)
	}
	return u.q.GetRuntimeDeployment(u.ctx)
}

func (u unit) LoadNode(id string) (deployment.StoredNode, error) {
	nodeID, err := parseID(id)
	if err != nil {
		return deployment.StoredNode{}, err
	}
	n, err := u.q.GetRuntimeNode(u.ctx, nodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.StoredNode{}, deployment.ErrNotFound
	}
	if err != nil {
		return deployment.StoredNode{}, err
	}
	return storedNode(n), nil
}

func (u unit) LoadEnrollment(tokenDigest string) (deployment.EnrollmentRecord, error) {
	r, err := u.q.GetRuntimeEnrollment(u.ctx, tokenDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.EnrollmentRecord{}, deployment.ErrNotFound
	}
	if err != nil {
		return deployment.EnrollmentRecord{}, err
	}
	return deployment.EnrollmentRecord{ID: uuidString(r.ID), InstallationID: uuidString(r.InstallationID), ExpiresAt: r.ExpiresAt.Time, Consumed: r.ConsumedAt.Valid, MaxActive: int(r.MaxActive), MaxRetained: int(r.MaxRetained)}, nil
}

func (u unit) LoadGenerationSpecification(generation uint64) (deployment.GenerationSpecification, error) {
	if generation > math.MaxInt64 {
		return deployment.GenerationSpecification{}, deployment.ErrInvalidInput
	}
	row, err := u.q.GetNodeGenerationSpecification(u.ctx, int64(generation))
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.GenerationSpecification{}, deployment.ErrNotFound
	}
	if err != nil {
		return deployment.GenerationSpecification{}, err
	}
	return deployment.GenerationSpecification{Provider: row.ProviderKind, Specification: row.Specification}, nil
}

func (u unit) GenerationKept(nodeID string, generation uint64) (bool, error) {
	id, err := parseID(nodeID)
	if err != nil {
		return false, err
	}
	if generation > math.MaxInt64 {
		return false, deployment.ErrInvalidInput
	}
	return u.q.NodeGenerationKept(u.ctx, sqlc.NodeGenerationKeptParams{NodeID: id, Generation: int64(generation)})
}

func (u unit) InsertNode(node deployment.NewNode) (deployment.StoredNode, error) {
	id, err := parseID(node.ID)
	if err != nil {
		return deployment.StoredNode{}, err
	}
	installation, err := parseID(node.InstallationID)
	if err != nil {
		return deployment.StoredNode{}, err
	}
	var enrollment pgtype.UUID
	if node.EnrollmentID != "" {
		if enrollment, err = parseID(node.EnrollmentID); err != nil {
			return deployment.StoredNode{}, err
		}
	}
	if node.DeploymentGeneration > math.MaxInt64 {
		return deployment.StoredNode{}, deployment.ErrInvalidInput
	}
	row, err := u.q.InsertRuntimeNode(u.ctx, sqlc.InsertRuntimeNodeParams{ID: id, InstallationID: installation, Name: node.Name, BackendFingerprint: node.BackendFingerprint, CredentialSha256: node.CredentialDigest, MaxActive: int32(node.MaxActive), MaxRetained: int32(node.MaxRetained), SpecificationDigest: node.SpecificationDigest, DeploymentGeneration: int64(node.DeploymentGeneration), CoreUrl: node.CoreURL, EnrollmentID: enrollment})
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" && databaseError.ConstraintName == "runtime_nodes_pkey" {
		return deployment.StoredNode{}, deployment.ErrNodeExists
	}
	if err != nil {
		return deployment.StoredNode{}, translate(err)
	}
	return storedNode(row), nil
}

func (u unit) UpdateNode(id string, limits deployment.NodeLimits) error {
	nodeID, err := parseID(id)
	if err != nil {
		return err
	}
	_, err = u.q.UpdateRuntimeNode(u.ctx, sqlc.UpdateRuntimeNodeParams{ID: nodeID, Name: limits.Name, MaxActive: int32(limits.MaxActive), MaxRetained: int32(limits.MaxRetained)})
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.ErrNotFound
	}
	return translate(err)
}

// nodeTx is one node management transaction.
type nodeTx struct{ unit }

var _ deployment.NodeTx = (*nodeTx)(nil)

func (t *nodeTx) ListNodes() ([]deployment.NodeRecord, error) {
	rows, err := t.q.ListRuntimeNodes(t.ctx, pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	return nodeRecords(rows)
}

func (t *nodeTx) RemoveNode(id string) error {
	nodeID, err := parseID(id)
	if err != nil {
		return err
	}
	return t.q.RemoveRuntimeNode(t.ctx, nodeID)
}

func (t *nodeTx) CreateEnrollment(enrollment deployment.NewEnrollment) (time.Time, error) {
	id, err := parseID(enrollment.ID)
	if err != nil {
		return time.Time{}, err
	}
	installation, err := parseID(enrollment.InstallationID)
	if err != nil {
		return time.Time{}, err
	}
	if err := t.q.CreateRuntimeEnrollment(t.ctx, sqlc.CreateRuntimeEnrollmentParams{ID: id, TokenSha256: enrollment.TokenDigest, InstallationID: installation, MaxActive: int32(enrollment.MaxActive), MaxRetained: int32(enrollment.MaxRetained)}); err != nil {
		return time.Time{}, err
	}
	row, err := t.q.GetRuntimeEnrollment(t.ctx, enrollment.TokenDigest)
	if err != nil {
		return time.Time{}, err
	}
	return row.ExpiresAt.Time, nil
}

func (t *nodeTx) ConsumeEnrollment(tokenDigest, nodeID string) (bool, error) {
	id, err := parseID(nodeID)
	if err != nil {
		return false, err
	}
	changed, err := t.q.ConsumeRuntimeEnrollment(t.ctx, sqlc.ConsumeRuntimeEnrollmentParams{TokenSha256: tokenDigest, NodeID: id})
	return changed == 1, err
}

func (t *nodeTx) HeartbeatNode(heartbeat deployment.Heartbeat) (bool, error) {
	id, err := parseID(heartbeat.NodeID)
	if err != nil {
		return false, err
	}
	connection, err := parseID(heartbeat.ConnectionID)
	if err != nil {
		return false, err
	}
	raw, err := json.Marshal(healthRecord{NodeHealth: heartbeat.Health, Host: heartbeat.Health.Host})
	if err != nil {
		return false, err
	}
	changed, err := t.q.HeartbeatRuntimeNode(t.ctx, sqlc.HeartbeatRuntimeNodeParams{ID: id, ConnectionID: connection, OwnerEpoch: int64(heartbeat.Epoch), ProviderReady: heartbeat.Health.ProviderReady, Health: raw})
	return changed == 1, translate(err)
}

func (t *nodeTx) DeleteGenerationStatus(nodeID string, generation uint64) error {
	id, err := parseID(nodeID)
	if err != nil {
		return err
	}
	return t.q.DeleteNodeGenerationStatus(t.ctx, sqlc.DeleteNodeGenerationStatusParams{NodeID: id, Generation: int64(generation)})
}

func (t *nodeTx) UpsertGenerationStatus(status deployment.GenerationStatusRecord) error {
	id, err := parseID(status.NodeID)
	if err != nil {
		return err
	}
	connection, err := parseID(status.ConnectionID)
	if err != nil {
		return err
	}
	return t.q.UpsertNodeGenerationStatus(t.ctx, sqlc.UpsertNodeGenerationStatusParams{NodeID: id, Generation: int64(status.Generation), SpecificationDigest: status.SpecificationDigest, ConnectionID: connection, OwnerEpoch: int64(status.OwnerEpoch), State: status.State, Diagnostic: status.Diagnostic})
}

func (t *nodeTx) PromoteServingGeneration(nodeID string, generation uint64) error {
	id, err := parseID(nodeID)
	if err != nil {
		return err
	}
	return t.q.PromoteNodeServingGeneration(t.ctx, sqlc.PromoteNodeServingGenerationParams{ID: id, ReadyGeneration: pgtype.Int8{Int64: int64(generation), Valid: true}})
}

func (t *nodeTx) RefreshServingReadiness(nodeID string, protocol int) error {
	id, err := parseID(nodeID)
	if err != nil {
		return err
	}
	return t.q.RefreshNodeServingReadiness(t.ctx, sqlc.RefreshNodeServingReadinessParams{ID: id, ProtocolVersion: int32(protocol)})
}

// deploymentTx is one leased deployment change.
type deploymentTx struct {
	unit
	cipher *credentialcrypto.Cipher
}

var _ deployment.DeploymentTx = (*deploymentTx)(nil)

// LoadDeployment opens the stored credential, which setup and switch need.
func (t *deploymentTx) LoadDeployment() (deployment.Record, error) {
	d, err := t.loadDeployment()
	if err != nil {
		return deployment.Record{}, err
	}
	return record(d, t.cipher, true), nil
}

func (t *deploymentTx) LoadSnapshot() (deployment.Snapshot, error) {
	row, err := t.q.GetSandboxDeploymentSnapshot(t.ctx)
	if err != nil {
		return deployment.Snapshot{}, err
	}
	return snapshot(row)
}

func (t *deploymentTx) CountResources() (deployment.Resources, error) {
	row, err := t.q.CountRuntimeDeploymentResources(t.ctx)
	if err != nil {
		return deployment.Resources{}, err
	}
	return deployment.Resources{Allocations: row.Allocations, Pending: row.Pending}, nil
}

func (t *deploymentTx) ClaimInstallation(installationID string) error {
	id, err := parseID(installationID)
	if err != nil {
		return err
	}
	return t.q.ClaimWebSandboxDeployment(t.ctx, id)
}

func (t *deploymentTx) SetProcessDeployment(installationID, backendFingerprint string, admissionPaused bool) error {
	id, err := parseID(installationID)
	if err != nil {
		return err
	}
	return t.q.SetRuntimeDeployment(t.ctx, sqlc.SetRuntimeDeploymentParams{InstallationID: id, BackendFingerprint: backendFingerprint, AdmissionPaused: admissionPaused})
}

func (t *deploymentTx) SetManagerDeployment(provider, localNodeID string) error {
	var local pgtype.UUID
	if localNodeID != "" {
		var err error
		if local, err = parseID(localNodeID); err != nil {
			return err
		}
	}
	return t.q.SetRuntimeManagerDeployment(t.ctx, sqlc.SetRuntimeManagerDeploymentParams{ProviderKind: provider, LocalNodeID: local})
}

func (t *deploymentTx) SaveSelection(selection deployment.SelectionRecord) error {
	if selection.Generation > math.MaxInt64 {
		return deployment.ErrInvalidInput
	}
	params := sqlc.InitializeSandboxDeploymentParams{ProviderKind: selection.Provider, BackendFingerprint: selection.BackendFingerprint, Generation: int64(selection.Generation), Mode: selection.Mode, IdleSeconds: selection.IdleSeconds, RetentionSeconds: selection.RetentionSeconds, ProviderConfig: selection.Configuration.Public, ProviderMetadata: selection.Configuration.Metadata, Specification: selection.Specification}
	if len(selection.Configuration.Secret) > 0 {
		if t.cipher == nil {
			return credentialcrypto.ErrUnavailable
		}
		sealed, err := t.cipher.SealSandboxDeployment(selection.Configuration.Secret, selection.InstallationID, selection.Generation)
		if err != nil {
			return errors.New("sandbox deployment credential encryption failed")
		}
		params.ProviderCredential = sealed
	}
	return translate(t.q.InitializeSandboxDeployment(t.ctx, params))
}

func (t *deploymentTx) RecordConfigurationMetadata(metadata json.RawMessage) error {
	return translate(t.q.RecordSandboxConfigurationMetadata(t.ctx, metadata))
}

func (t *deploymentTx) RetainGeneration() error { return t.q.RetainSandboxGeneration(t.ctx) }

func (t *deploymentTx) CollectGenerations() error { return t.q.CollectSandboxGenerations(t.ctx) }

func (t *deploymentTx) StartReset(clear string, deadlineSeconds int32, source adminaudit.Source) error {
	// The audit entry recorded in the same transaction validates the source
	// before commit. Only the typed, non-secret source is stored.
	audit, err := json.Marshal(source)
	if err != nil {
		return err
	}
	return t.q.StartSandboxReset(t.ctx, sqlc.StartSandboxResetParams{Clear: pgtype.Text{String: clear, Valid: true}, DeadlineSeconds: deadlineSeconds, Audit: audit})
}

func (t *deploymentTx) ForceReset() error { return t.q.ForceSandboxReset(t.ctx) }

func (t *deploymentTx) CancelReset() error { return t.q.CancelSandboxReset(t.ctx) }

func (t *deploymentTx) LoadResetSource() (adminaudit.Source, error) {
	d, err := t.loadDeployment()
	if err != nil {
		return adminaudit.Source{}, err
	}
	return ResetSource(d)
}

var errNoResetSource = errors.New("sandbox reset has no stored administrator source")

// ResetSource decodes the administrator source that started the running reset
// from the stored deployment row. Audit entries the reset records later carry
// that source.
func ResetSource(d sqlc.RuntimeDeployment) (adminaudit.Source, error) {
	var source adminaudit.Source
	if !d.ResetClear.Valid || json.Unmarshal(d.ResetAudit, &source) != nil {
		return adminaudit.Source{}, errNoResetSource
	}
	return source, nil
}

func (t *deploymentTx) CompleteReset() error {
	if err := t.q.CompleteSandboxReset(t.ctx); err != nil {
		return err
	}
	if err := t.q.RetireSandboxNodes(t.ctx); err != nil {
		return err
	}
	if err := t.q.RetireSandboxEnrollments(t.ctx); err != nil {
		return err
	}
	return t.q.ClearSandboxGenerations(t.ctx)
}

func (t *deploymentTx) RecordAudit(action, installationID string) error {
	return auditpg.RecordDeploymentMutation(t.ctx, t.q, action, "sandbox_deployment", installationID)
}

func (t *deploymentTx) RecordAuditAs(source adminaudit.Source, action, installationID string) error {
	return auditpg.RecordDeploymentMutation(adminaudit.WithSource(t.ctx, source), t.q, action, "sandbox_deployment", installationID)
}
