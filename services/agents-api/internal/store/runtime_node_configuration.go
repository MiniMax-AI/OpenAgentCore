package store

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
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
	var result RuntimeNodeConfiguration
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
		if d.Mode != "nodes" {
			return ErrSandboxDeploymentConflict
		}
		spec, err := deploymentSpecification(d)
		if err != nil {
			return err
		}
		if node == nil && d.Maintenance {
			return ErrSandboxDeploymentConflict
		}
		if node != nil && (node.DeploymentGeneration != d.Generation || node.SpecificationDigest != spec.Digest(d.ProviderKind)) {
			return ErrRuntimeSpecificationMismatch
		}
		result = RuntimeNodeConfiguration{MaxActive: int(active), MaxRetained: retainedLimit(d.ProviderKind, int(active), int(retained)), InstallationID: runtimeUUID(d.InstallationID), Provider: d.ProviderKind, CoreURL: d.CoreUrl, Generation: uint64(d.Generation), Specification: spec, SpecificationDigest: spec.Digest(d.ProviderKind)}
		return nil
	})
	return result, err
}
