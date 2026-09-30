package store

import (
	"context"
	"math"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/providers"
	"github.com/jackc/pgx/v5/pgtype"
)

type RuntimeNodeConfiguration struct {
	MaxActive           int                    `json:"max_active"`
	MaxRetained         int                    `json:"max_retained"`
	InstallationID      string                 `json:"installation_id"`
	Provider            string                 `json:"provider"`
	CoreURL             string                 `json:"core_url"`
	Generation          uint64                 `json:"generation"`
	Specification       sandbox.DeploymentSpec `json:"specification"`
	SpecificationDigest string                 `json:"specification_digest"`
}

// Read-only bootstrap never consumes enrollment tokens or reveals cloud credentials.
// The deployment lock keeps authentication and the returned generation consistent.
// The credential is authenticated before any deployment state is reported.
func (s *Store) RuntimeNodeConfiguration(ctx context.Context, nodeID, token string) (RuntimeNodeConfiguration, error) {
	return s.RuntimeNodeGenerationConfiguration(ctx, nodeID, token, 0)
}

// Exact generation recovery is restricted to this node's current target, serving
// pin and unreleased ownership. It is never a general history read.
func (s *Store) RuntimeNodeGenerationConfiguration(ctx context.Context, nodeID, token string, generation uint64) (RuntimeNodeConfiguration, error) {
	var result RuntimeNodeConfiguration
	if generation > math.MaxInt64 {
		return result, ErrInvalidInput
	}
	err := s.runtimeDeploymentTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		var node *sqlc.RuntimeNode
		var installation pgtype.UUID
		var active, retained int32
		if nodeID == "" {
			r, err := q.GetRuntimeEnrollment(ctx, runtimeTokenDigest(token))
			if err != nil || r.ConsumedAt.Valid || !r.ExpiresAt.Time.After(time.Now()) {
				return ErrRuntimeNodeCredential
			}
			installation, active, retained = r.InstallationID, r.MaxActive, r.MaxRetained
		} else {
			id, err := parseConnectionGeneration(nodeID)
			if err != nil {
				return ErrRuntimeNodeCredential
			}
			n, err := q.GetRuntimeNode(ctx, id)
			if err != nil || n.CredentialSha256 != runtimeTokenDigest(token) {
				return ErrRuntimeNodeCredential
			}
			node, installation, active, retained = &n, n.InstallationID, n.MaxActive, n.MaxRetained
		}
		// A claimed installation rejects foreign credentials identically before
		// and after initialization.
		if d.InstallationID.Valid && installation != d.InstallationID {
			return ErrRuntimeNodeCredential
		}
		if !runtimeDeploymentInitialized(d) {
			return ErrRuntimeNodeUnavailable
		}
		if node == nil && d.ResetClear.Valid {
			return ErrSandboxResetInProgress
		}
		if d.Mode != "nodes" {
			return ErrSandboxDeploymentConflict
		}
		if node != nil {
			if err := validateNodeEnrollmentIdentity(ctx, q, d, *node); err != nil {
				return err
			}
		}
		selected := uint64(d.Generation)
		if generation != 0 {
			if node == nil {
				return ErrRuntimeNodeCredential
			}
			selected = generation
			kept, err := q.NodeGenerationKept(ctx, sqlc.NodeGenerationKeptParams{NodeID: node.ID, Generation: int64(generation)})
			if err != nil {
				return err
			}
			if !kept {
				return ErrRuntimeSpecificationMismatch
			}
		}
		spec, err := nodeGenerationSpec(ctx, q, d, selected)
		if err != nil {
			return err
		}
		if node == nil && d.AdmissionPaused {
			return ErrSandboxDeploymentConflict
		}
		result = RuntimeNodeConfiguration{MaxActive: int(active), MaxRetained: providers.RetainedLimit(d.ProviderKind, int(active), int(retained)), InstallationID: runtimeUUID(d.InstallationID), Provider: d.ProviderKind, CoreURL: s.publicURL, Generation: selected, Specification: spec, SpecificationDigest: spec.Digest(d.ProviderKind)}
		return nil
	})
	return result, err
}
