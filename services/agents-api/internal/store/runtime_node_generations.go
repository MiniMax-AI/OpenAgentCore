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

func nodeGenerationSpec(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment, generation uint64) (sandbox.DeploymentSpec, error) {
	var spec sandbox.DeploymentSpec
	if generation == 0 || generation > math.MaxInt64 {
		return spec, ErrInvalidInput
	}
	row, err := q.GetNodeGenerationSpecification(ctx, int64(generation))
	if err != nil {
		return spec, err
	}
	if row.ProviderKind != d.ProviderKind || json.Unmarshal(row.Specification, &spec) != nil || providers.ValidateSpecification(d.ProviderKind, spec) != nil {
		return spec, ErrRuntimeSpecificationMismatch
	}
	return spec, nil
}

func nodeConnection(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment, nodeID, connectionID string, epoch uint64) (sqlc.RuntimeNode, error) {
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return sqlc.RuntimeNode{}, ErrRuntimeNodeCredential
	}
	n, err := q.GetRuntimeNode(ctx, id)
	if err != nil {
		return n, err
	}
	if runtimeUUID(n.ConnectionID) != connectionID || n.ConnectedEpoch != int64(epoch) || epoch == 0 || epoch > math.MaxInt64 || d.OwnerEpoch != int64(epoch) || n.InstallationID != d.InstallationID || d.Mode != "nodes" {
		return n, ErrRuntimeNodeCredential
	}
	return n, nil
}

// RuntimeNodeRetention is a bounded read under the same serialization as pin
// promotion and placement. A drop cannot subsequently gain fresh old ownership.
func (s *Store) RuntimeNodeRetention(ctx context.Context, nodeID, connectionID string, epoch uint64, refs []sandbox.GenerationReference) (sandbox.NodeDeployment, []sandbox.GenerationRetention, error) {
	var deployment sandbox.NodeDeployment
	grants := make([]sandbox.GenerationRetention, 0, len(refs))
	if len(refs) > 8 {
		return deployment, nil, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	defer cancel()
	err := s.runtimeDeploymentTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		n, err := nodeConnection(ctx, q, d, nodeID, connectionID, epoch)
		if err != nil {
			return err
		}
		deployment.Generation = uint64(d.Generation)
		spec, err := nodeGenerationSpec(ctx, q, d, uint64(d.Generation))
		if err != nil {
			return err
		}
		deployment.SpecificationDigest = spec.Digest(d.ProviderKind)
		if n.ReadyGeneration.Valid {
			pin := uint64(n.ReadyGeneration.Int64)
			deployment.ServingGeneration = &pin
		}
		seen := map[uint64]bool{}
		for _, ref := range refs {
			if ref.Generation == 0 || ref.Generation > math.MaxInt64 || !validRuntimeDigest(ref.SpecificationDigest) || seen[ref.Generation] {
				return ErrInvalidInput
			}
			seen[ref.Generation] = true
			kept, err := q.NodeGenerationKept(ctx, sqlc.NodeGenerationKeptParams{NodeID: n.ID, Generation: int64(ref.Generation)})
			if err != nil {
				return err
			}
			if kept {
				spec, err := nodeGenerationSpec(ctx, q, d, ref.Generation)
				if err != nil {
					return err
				}
				if spec.Digest(d.ProviderKind) != ref.SpecificationDigest {
					return ErrRuntimeSpecificationMismatch
				}
			}
			if !kept {
				if err := q.DeleteNodeGenerationStatus(ctx, sqlc.DeleteNodeGenerationStatusParams{NodeID: n.ID, Generation: int64(ref.Generation)}); err != nil {
					return err
				}
			}
			grants = append(grants, sandbox.GenerationRetention{GenerationReference: ref, Keep: kept})
		}
		return nil
	})
	if err != nil {
		return sandbox.NodeDeployment{}, nil, err
	}
	return deployment, grants, nil
}

func (s *Store) HeartbeatRuntimeNodeGenerations(ctx context.Context, nodeID, connectionID string, epoch uint64, health RuntimeNodeHealth, statuses []sandbox.GenerationStatus) error {
	if len(statuses) > 8 {
		return ErrInvalidInput
	}
	return s.heartbeatRuntimeNode(ctx, nodeID, connectionID, epoch, health, statuses, 2)
}

func recordNodeGenerations(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment, n sqlc.RuntimeNode, statuses []sandbox.GenerationStatus, protocol int32) error {
	seen := map[uint64]bool{}
	for _, status := range statuses {
		if status.Generation == 0 || status.Generation > math.MaxInt64 || !validRuntimeDigest(status.SpecificationDigest) || seen[status.Generation] || (status.State != "ready" && status.State != "preparing" && status.State != "failed") || status.State == "ready" && status.Diagnostic != "" {
			return ErrInvalidInput
		}
		seen[status.Generation] = true
		kept, err := q.NodeGenerationKept(ctx, sqlc.NodeGenerationKeptParams{NodeID: n.ID, Generation: int64(status.Generation)})
		if err != nil {
			return err
		}
		// A sparse report may race collection of a skipped target. It creates no
		// readiness or pin; only the later correlated retention reply can drop it.
		if !kept {
			continue
		}
		spec, err := nodeGenerationSpec(ctx, q, d, status.Generation)
		if err != nil {
			return err
		}
		if spec.Digest(d.ProviderKind) != status.SpecificationDigest {
			return ErrRuntimeSpecificationMismatch
		}
		diagnostic := sandbox.NormalizeNodeDiagnostic(status.Diagnostic)
		if err := q.UpsertNodeGenerationStatus(ctx, sqlc.UpsertNodeGenerationStatusParams{NodeID: n.ID, Generation: int64(status.Generation), SpecificationDigest: status.SpecificationDigest, ConnectionID: n.ConnectionID, OwnerEpoch: d.OwnerEpoch, State: status.State, Diagnostic: diagnostic}); err != nil {
			return err
		}
		if status.State == "ready" && status.Generation == uint64(d.Generation) {
			if err := q.PromoteNodeServingGeneration(ctx, sqlc.PromoteNodeServingGenerationParams{ID: n.ID, ReadyGeneration: pgtype.Int8{Int64: int64(status.Generation), Valid: true}}); err != nil {
				return err
			}
		}
	}
	return q.RefreshNodeServingReadiness(ctx, sqlc.RefreshNodeServingReadinessParams{ID: n.ID, ProtocolVersion: protocol})
}

// Enrollment identity survives collection of its old specification. Whenever
// that specification is still authoritative, its exact digest must agree.
func validateNodeEnrollmentIdentity(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment, n sqlc.RuntimeNode) error {
	if n.DeploymentGeneration <= 0 || n.DeploymentGeneration > d.Generation || !validRuntimeDigest(n.SpecificationDigest) {
		return ErrRuntimeSpecificationMismatch
	}
	spec, err := nodeGenerationSpec(ctx, q, d, uint64(n.DeploymentGeneration))
	if errors.Is(err, pgx.ErrNoRows) && n.DeploymentGeneration < d.Generation {
		return nil
	}
	if err != nil || spec.Digest(d.ProviderKind) != n.SpecificationDigest {
		return ErrRuntimeSpecificationMismatch
	}
	return nil
}
