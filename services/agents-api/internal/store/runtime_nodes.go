package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func runtimeTokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
func validRuntimeDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
func validateRuntimeNode(name string, active, retained int) error {
	if strings.TrimSpace(name) == "" || len(name) > 128 || strings.ContainsAny(name, "\x00\r\n") || active < 1 || retained < active || retained > 1000000 {
		return ErrInvalidInput
	}
	return nil
}
func (s *Store) runtimeManagerTransaction(ctx context.Context, apply func(*sqlc.Queries, sqlc.RuntimeDeployment) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		deployment, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		if !deployment.InstallationID.Valid || deployment.ProviderKind == "" {
			return ErrRuntimeNodeUnavailable
		}
		return apply(q, deployment)
	})
}
func (s *Store) GetRuntimeDeployment(ctx context.Context) (RuntimeDeploymentView, error) {
	return getRuntimeDeploymentView(ctx, s.queries)
}
func getRuntimeDeploymentView(ctx context.Context, q *sqlc.Queries) (RuntimeDeploymentView, error) {
	d, err := q.GetRuntimeDeployment(ctx)
	if err != nil {
		return RuntimeDeploymentView{}, err
	}
	resources, err := q.CountRuntimeDeploymentResources(ctx)
	if err != nil {
		return RuntimeDeploymentView{}, err
	}
	result := runtimeDeploymentView(d)
	result.Resources = SandboxDeploymentResources{Allocations: resources.Allocations, Pending: resources.Pending}
	return result, nil
}
func (s *Store) RuntimeOwnerEpoch(ctx context.Context) (uint64, error) {
	d, err := s.queries.GetRuntimeDeployment(ctx)
	return uint64(d.OwnerEpoch), err
}
func runtimeUUID(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
func (s *Store) ListRuntimeNodes(ctx context.Context) ([]RuntimeNode, error) {
	rows, err := s.queries.ListRuntimeNodes(ctx, pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	return runtimeNodeViews(rows)
}

func runtimeNodeViews(rows []sqlc.ListRuntimeNodesRow) ([]RuntimeNode, error) {
	out := make([]RuntimeNode, 0, len(rows))
	for _, n := range rows {
		var seen *time.Time
		if n.LastSeenAt.Valid {
			value := n.LastSeenAt.Time
			seen = &value
		}
		var health RuntimeNodeHealth
		if err := json.Unmarshal(n.Health, &health); err != nil {
			return nil, err
		}
		health.ProviderReady = n.ProviderReady
		out = append(out, RuntimeNode{RuntimeNodeHealth: health, Running: n.Running, Snapshots: n.Snapshots, ID: runtimeUUID(n.ID), Name: n.Name, Provider: n.ProviderKind, Online: n.Online, LastSeenAt: seen, MaxActive: int(n.MaxActive), MaxRetained: int(n.MaxRetained), Active: n.Active, Reserved: n.Reserved, Retained: n.Retained, CleanupPending: n.CleanupPending, CreatedAt: n.CreatedAt.Time})
	}
	return out, nil
}
func (s *Store) CreateRuntimeEnrollment(ctx context.Context, capacity RuntimeNodeCapacity) (string, time.Time, error) {
	if err := validateRuntimeNode("enrollment", capacity.MaxActive, capacity.MaxRetained); err != nil {
		return "", time.Time{}, err
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(bytes[:])
	var expires time.Time
	err := s.runtimeManagerTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if d.Mode != "nodes" || d.Maintenance || unspecifiedNodeDeployment(d) {
			return ErrSandboxDeploymentConflict
		}
		if err := q.CreateRuntimeEnrollment(ctx, sqlc.CreateRuntimeEnrollmentParams{TokenSha256: runtimeTokenDigest(token), InstallationID: d.InstallationID, MaxActive: int32(capacity.MaxActive), MaxRetained: int32(capacity.MaxRetained)}); err != nil {
			return err
		}
		row, err := q.GetRuntimeEnrollment(ctx, runtimeTokenDigest(token))
		expires = row.ExpiresAt.Time
		return err
	})
	return token, expires, err
}
func (s *Store) EnrollRuntimeNode(ctx context.Context, token string, input RuntimeNodeEnrollment) (RuntimeNodeIdentity, error) {
	id, err := parseConnectionGeneration(input.NodeID)
	if err != nil || len(input.Credential) < 32 || len(input.Credential) > 256 || strings.ContainsAny(input.Credential, " \t\r\n") || !validRuntimeDigest(input.BackendFingerprint) {
		return RuntimeNodeIdentity{}, ErrInvalidInput
	}
	if err := validateRuntimeNode(input.Name, 1, 1); err != nil {
		return RuntimeNodeIdentity{}, err
	}
	var result RuntimeNodeIdentity
	err = s.runtimeManagerTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		receipt, err := q.GetRuntimeEnrollment(ctx, runtimeTokenDigest(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRuntimeNodeCredential
		}
		if err != nil {
			return ErrRuntimeNodeUnavailable
		}
		if receipt.ConsumedAt.Valid || !receipt.ExpiresAt.Time.After(time.Now()) || receipt.InstallationID != d.InstallationID {
			return ErrRuntimeNodeCredential
		}
		if d.Mode != "nodes" || d.Maintenance || input.Provider != d.ProviderKind {
			return ErrInvalidInput
		}
		spec, err := deploymentSpecification(d)
		if err != nil || input.DeploymentGeneration != uint64(d.Generation) || input.SpecificationDigest != spec.Digest(d.ProviderKind) {
			return ErrRuntimeSpecificationMismatch
		}
		if _, err := q.GetRuntimeNode(ctx, id); err == nil {
			return ErrIdempotencyConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		row, err := q.InsertRuntimeNode(ctx, sqlc.InsertRuntimeNodeParams{ID: id, InstallationID: d.InstallationID, Name: input.Name, BackendFingerprint: input.BackendFingerprint, CredentialSha256: runtimeTokenDigest(input.Credential), MaxActive: receipt.MaxActive, MaxRetained: receipt.MaxRetained, SpecificationDigest: input.SpecificationDigest, DeploymentGeneration: int64(input.DeploymentGeneration)})
		if err != nil {
			return err
		}
		changed, err := q.ConsumeRuntimeEnrollment(ctx, sqlc.ConsumeRuntimeEnrollmentParams{TokenSha256: runtimeTokenDigest(token), NodeID: id})
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrRuntimeNodeCredential
		}
		result = nodeIdentity(row, d.ProviderKind)
		return nil
	})
	return result, err
}
func nodeIdentity(n sqlc.RuntimeNode, kind string) RuntimeNodeIdentity {
	return RuntimeNodeIdentity{SpecificationDigest: n.SpecificationDigest, DeploymentGeneration: uint64(n.DeploymentGeneration), NodeID: runtimeUUID(n.ID), InstallationID: runtimeUUID(n.InstallationID), Provider: kind, BackendFingerprint: n.BackendFingerprint, MaxActive: int(n.MaxActive), MaxRetained: int(n.MaxRetained)}
}
func (s *Store) AuthenticateRuntimeNode(ctx context.Context, nodeID, credential string) (RuntimeNodeIdentity, error) {
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return RuntimeNodeIdentity{}, ErrRuntimeNodeCredential
	}
	n, err := s.queries.GetRuntimeNode(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeNodeIdentity{}, ErrRuntimeNodeCredential
	}
	if err != nil {
		return RuntimeNodeIdentity{}, ErrRuntimeNodeUnavailable
	}
	if n.CredentialSha256 != runtimeTokenDigest(credential) {
		return RuntimeNodeIdentity{}, ErrRuntimeNodeCredential
	}
	d, err := s.queries.GetRuntimeDeployment(ctx)
	if err != nil {
		return RuntimeNodeIdentity{}, ErrRuntimeNodeUnavailable
	}
	if n.InstallationID != d.InstallationID || d.Mode != "nodes" {
		return RuntimeNodeIdentity{}, ErrRuntimeNodeCredential
	}
	if unspecifiedNodeDeployment(d) {
		if n.SpecificationDigest != "" || n.DeploymentGeneration != 0 {
			return RuntimeNodeIdentity{}, ErrRuntimeSpecificationMismatch
		}
		return nodeIdentity(n, d.ProviderKind), nil
	}
	spec, err := deploymentSpecification(d)
	if err != nil || n.SpecificationDigest != spec.Digest(d.ProviderKind) || n.DeploymentGeneration != d.Generation {
		return RuntimeNodeIdentity{}, ErrRuntimeSpecificationMismatch
	}
	return nodeIdentity(n, d.ProviderKind), nil
}
func (s *Store) UpdateRuntimeNode(ctx context.Context, nodeID string, input RuntimeNodeUpdate) error {
	if err := validateRuntimeNode(input.Name, input.MaxActive, input.MaxRetained); err != nil {
		return err
	}
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return err
	}
	return s.runtimeManagerTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		n, err := q.GetRuntimeNode(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if n.InstallationID != d.InstallationID {
			return ErrNotFound
		}
		_, err = q.UpdateRuntimeNode(ctx, sqlc.UpdateRuntimeNodeParams{ID: id, Name: input.Name, MaxActive: int32(input.MaxActive), MaxRetained: int32(input.MaxRetained)})
		return err
	})
}
func (s *Store) RemoveRuntimeNode(ctx context.Context, nodeID string) error {
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return err
	}
	return s.runtimeManagerTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		rows, err := q.ListRuntimeNodes(ctx, pgtype.UUID{})
		if err != nil {
			return err
		}
		for _, n := range rows {
			if n.ID != id {
				continue
			}
			if n.Retained != 0 || n.CleanupPending != 0 {
				return ErrRuntimeNodeInUse
			}
			if d.LocalNodeID == id {
				return ErrRuntimeLocalNodeConfigured
			}
			return q.RemoveRuntimeNode(ctx, id)
		}
		return ErrNotFound
	})
}
func (s *Store) ListNodeRuntimeAllocations(ctx context.Context, nodeID string) ([]RuntimeNodeAllocation, error) {
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return nil, err
	}
	if _, err := s.queries.GetRuntimeNode(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListNodeRuntimeAllocations(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]RuntimeNodeAllocation, 0, len(rows))
	for _, a := range rows {
		out = append(out, RuntimeNodeAllocation{Diagnostic: a.ObservationError, ID: runtimeUUID(a.ID), NodeID: runtimeUUID(a.NodeID), TenantID: runtimeUUID(a.TenantID), SessionID: runtimeUUID(a.SessionID), EnvironmentID: runtimeUUID(a.EnvironmentID), State: a.State, ComputePhase: a.ComputePhase, Initialization: a.Initialization, CreatedAt: a.CreatedAt.Time})
	}
	return out, nil
}
