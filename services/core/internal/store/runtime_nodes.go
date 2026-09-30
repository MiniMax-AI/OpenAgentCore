package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
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
	if strings.TrimSpace(name) == "" || len(name) > 128 || strings.ContainsAny(name, "\x00\r\n") {
		return &AdminValidationError{Code: "invalid_name", Param: "name", MaxLength: 128, message: ErrInvalidInput.Error()}
	}
	if active < 1 || active > 1000000 {
		return &AdminValidationError{Code: "invalid_node_capacity", Param: "max_active", message: ErrInvalidInput.Error()}
	}
	if retained < active || retained > 1000000 {
		return &AdminValidationError{Code: "invalid_node_capacity", Param: "max_retained", message: ErrInvalidInput.Error()}
	}
	return nil
}
func (s *Store) runtimeManagerTransaction(ctx context.Context, apply func(*sqlc.Queries, sqlc.RuntimeDeployment) error) error {
	return s.runtimeDeploymentTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if !runtimeDeploymentInitialized(d) {
			return ErrRuntimeNodeUnavailable
		}
		return apply(q, d)
	})
}

// runtimeDeploymentTransaction locks the deployment whether or not it is
// initialized. Node machine routes use it to authenticate their credential
// before reporting any deployment state, including an uninitialized one.
func (s *Store) runtimeDeploymentTransaction(ctx context.Context, apply func(*sqlc.Queries, sqlc.RuntimeDeployment) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		deployment, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		return apply(q, deployment)
	})
}

func runtimeDeploymentInitialized(d sqlc.RuntimeDeployment) bool {
	return d.InstallationID.Valid && d.ProviderKind != ""
}
func (s *Store) GetRuntimeDeployment(ctx context.Context) (RuntimeDeploymentView, error) {
	return s.deploymentView(ctx, s.queries)
}

// deploymentView reports the public URL as the deployment's read-only core_url.
func (s *Store) deploymentView(ctx context.Context, q *sqlc.Queries) (RuntimeDeploymentView, error) {
	row, err := q.GetSandboxDeploymentSnapshot(ctx)
	if err != nil {
		return RuntimeDeploymentView{}, err
	}
	d := row.RuntimeDeployment
	result := runtimeDeploymentView(d, s.publicURL)
	if err := json.Unmarshal(row.Rollout, &result.Rollout); err != nil {
		return RuntimeDeploymentView{}, err
	}
	result.Resources = SandboxDeploymentResources{Allocations: row.Allocations, Pending: row.Pending}
	if d.ResetClear.Valid {
		remaining := SandboxResetRemaining{}
		if err := json.Unmarshal(row.Remaining, &remaining); err != nil {
			return RuntimeDeploymentView{}, err
		}
		result.Reset = &SandboxResetView{Clear: d.ResetClear.String, RequestedAt: d.ResetRequestedAt.Time,
			DeadlineAt: resetTimestamp(d.ResetDeadlineAt), ForcedAt: resetTimestamp(d.ResetForcedAt), Remaining: remaining}
	}
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

func optionalUUID(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	value := runtimeUUID(id)
	return &value
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
		health.ProviderReady = n.Online && n.ServingReady
		out = append(out, RuntimeNode{Rollout: nodeRollout(n), RuntimeNodeHealth: health, Running: n.Running, Snapshots: n.Snapshots, ID: runtimeUUID(n.ID), Name: n.Name, CoreURL: n.CoreUrl, EnrollmentID: optionalUUID(n.EnrollmentID), Provider: n.ProviderKind, Online: n.Online, LastSeenAt: seen, MaxActive: int(n.MaxActive), MaxRetained: providers.RetainedLimit(n.ProviderKind, int(n.MaxActive), int(n.MaxRetained)), Active: n.Active, Reserved: n.Reserved, Retained: n.Retained, CleanupPending: n.CleanupPending, CreatedAt: n.CreatedAt.Time})
	}
	return out, nil
}

// CreateRuntimeEnrollment issues a one-use enrollment token. Its ID is a public,
// non-secret handle: the node the token registers reports it as enrollment_id.
func (s *Store) CreateRuntimeEnrollment(ctx context.Context, capacity RuntimeNodeCapacity) (RuntimeNodeEnrollmentToken, error) {
	// The retained limit depends on the provider, so the transaction checks it.
	if err := validateRuntimeNode("enrollment", capacity.MaxActive, capacity.MaxActive); err != nil {
		return RuntimeNodeEnrollmentToken{}, err
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return RuntimeNodeEnrollmentToken{}, err
	}
	id := uuid.New()
	result := RuntimeNodeEnrollmentToken{Token: hex.EncodeToString(bytes[:]), ID: id.String()}
	err := s.runtimeManagerTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		capacity.MaxRetained = providers.RetainedLimit(d.ProviderKind, capacity.MaxActive, capacity.MaxRetained)
		if err := validateRuntimeNode("enrollment", capacity.MaxActive, capacity.MaxRetained); err != nil {
			return err
		}
		if d.ResetClear.Valid {
			return ErrSandboxResetInProgress
		}
		if d.Mode != "nodes" || d.AdmissionPaused {
			return ErrSandboxDeploymentConflict
		}
		if _, err := deploymentSpecification(d); err != nil {
			return ErrSandboxDeploymentConflict
		}
		if err := q.CreateRuntimeEnrollment(ctx, sqlc.CreateRuntimeEnrollmentParams{ID: pgtype.UUID{Bytes: id, Valid: true}, TokenSha256: runtimeTokenDigest(result.Token), InstallationID: d.InstallationID, MaxActive: int32(capacity.MaxActive), MaxRetained: int32(capacity.MaxRetained)}); err != nil {
			return err
		}
		row, err := q.GetRuntimeEnrollment(ctx, runtimeTokenDigest(result.Token))
		result.ExpiresAt = row.ExpiresAt.Time
		return err
	})
	return result, err
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
	err = s.runtimeDeploymentTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		receipt, err := q.GetRuntimeEnrollment(ctx, runtimeTokenDigest(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRuntimeNodeCredential
		}
		if err != nil {
			return ErrRuntimeNodeUnavailable
		}
		if receipt.ConsumedAt.Valid || !receipt.ExpiresAt.Time.After(time.Now()) {
			return ErrRuntimeNodeCredential
		}
		if d.InstallationID.Valid && receipt.InstallationID != d.InstallationID {
			return ErrRuntimeNodeCredential
		}
		if !runtimeDeploymentInitialized(d) {
			return ErrRuntimeNodeUnavailable
		}
		if d.ResetClear.Valid {
			return ErrSandboxResetInProgress
		}
		if d.Mode != "nodes" || d.AdmissionPaused || input.Provider != d.ProviderKind {
			return ErrInvalidInput
		}
		spec, err := deploymentSpecification(d)
		if err != nil || input.DeploymentGeneration != uint64(d.Generation) || input.SpecificationDigest != spec.Digest(d.ProviderKind) {
			return ErrRuntimeSpecificationMismatch
		}
		// The node must use the address Core advertises now. It read that address
		// from its configuration, but the public URL may have changed since, or an
		// operator may have registered by hand with another origin.
		if input.CoreURL != s.publicURL {
			return ErrRuntimeNodeAddressMismatch
		}
		if _, err := q.GetRuntimeNode(ctx, id); err == nil {
			return ErrIdempotencyConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		row, err := q.InsertRuntimeNode(ctx, sqlc.InsertRuntimeNodeParams{ID: id, InstallationID: d.InstallationID, Name: input.Name, BackendFingerprint: input.BackendFingerprint, CredentialSha256: runtimeTokenDigest(input.Credential), MaxActive: receipt.MaxActive, MaxRetained: int32(providers.RetainedLimit(d.ProviderKind, int(receipt.MaxActive), int(receipt.MaxRetained))), SpecificationDigest: input.SpecificationDigest, DeploymentGeneration: int64(input.DeploymentGeneration), CoreUrl: input.CoreURL, EnrollmentID: receipt.ID})
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
	return RuntimeNodeIdentity{SpecificationDigest: n.SpecificationDigest, DeploymentGeneration: uint64(n.DeploymentGeneration), NodeID: runtimeUUID(n.ID), InstallationID: runtimeUUID(n.InstallationID), Provider: kind, BackendFingerprint: n.BackendFingerprint, MaxActive: int(n.MaxActive), MaxRetained: providers.RetainedLimit(kind, int(n.MaxActive), int(n.MaxRetained))}
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
	if err := validateNodeEnrollmentIdentity(ctx, s.queries, d, n); err != nil {
		return RuntimeNodeIdentity{}, err
	}
	// Reset retires nodes before another backend lineage can be selected.
	// Enrollment generation and digest remain immutable identity history; the
	// current target and per-generation readiness do not replace that history.
	if n.DeploymentGeneration <= 0 || !validRuntimeDigest(n.SpecificationDigest) {
		return RuntimeNodeIdentity{}, ErrRuntimeSpecificationMismatch
	}
	return nodeIdentity(n, d.ProviderKind), nil
}
func (s *Store) UpdateRuntimeNode(ctx context.Context, nodeID string, input RuntimeNodeUpdate) error {
	// The retained limit depends on the provider, so the transaction checks it.
	if err := validateRuntimeNode(input.Name, input.MaxActive, input.MaxActive); err != nil {
		return err
	}
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return err
	}
	return s.runtimeManagerTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		input.MaxRetained = providers.RetainedLimit(d.ProviderKind, input.MaxActive, input.MaxRetained)
		if err := validateRuntimeNode(input.Name, input.MaxActive, input.MaxRetained); err != nil {
			return err
		}
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
		var phaseChanged *time.Time
		if a.ComputePhaseChangedAt.Valid {
			value := a.ComputePhaseChangedAt.Time
			phaseChanged = &value
		}
		out = append(out, RuntimeNodeAllocation{DeploymentGeneration: uint64(a.DeploymentGeneration.Int64), Diagnostic: a.ObservationError, ID: runtimeUUID(a.ID), NodeID: runtimeUUID(a.NodeID), TenantID: runtimeUUID(a.TenantID), SessionID: runtimeUUID(a.SessionID), EnvironmentID: runtimeUUID(a.EnvironmentID), State: a.State, ComputePhase: a.ComputePhase, ComputePhaseChangedAt: phaseChanged, Initialization: a.Initialization, CreatedAt: a.CreatedAt.Time})
	}
	return out, nil
}

func nodeRollout(n sqlc.ListRuntimeNodesRow) SandboxNodeRollout {
	out := SandboxNodeRollout{State: "unknown"}
	if n.ReadyGeneration.Valid {
		generation := uint64(n.ReadyGeneration.Int64)
		out.ReadyGeneration = &generation
	}
	if !n.Online {
		return out
	}
	if n.ProtocolVersion == 1 && n.DeploymentGeneration != n.TargetGeneration {
		out.State = "update_required"
		return out
	}
	switch n.TargetState {
	case "ready", "preparing", "failed":
		out.State = n.TargetState
	}
	if out.State == "failed" && n.TargetDiagnostic != "" {
		out.Diagnostic = sandbox.NormalizeNodeDiagnostic(n.TargetDiagnostic)
	}
	return out
}
