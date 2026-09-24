package store

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

type RuntimeNodeConfiguration struct {
	InstallationID      string                 `json:"installation_id"`
	Provider            string                 `json:"provider"`
	CoreURL             string                 `json:"core_url"`
	Generation          uint64                 `json:"generation"`
	Specification       sandbox.DeploymentSpec `json:"specification"`
	SpecificationDigest string                 `json:"specification_digest"`
}

// Read-only bootstrap never consumes enrollment tokens or reveals cloud credentials.
// The deployment lock keeps authentication and the returned generation consistent.
func (s *Store) RuntimeNodeConfiguration(ctx context.Context, nodeID, token string) (RuntimeNodeConfiguration, error) {
	var result RuntimeNodeConfiguration
	err := s.runtimeManagerTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if d.Mode != "nodes" {
			return ErrSandboxDeploymentConflict
		}
		spec, err := deploymentSpecification(d)
		if err != nil {
			return err
		}
		if nodeID == "" {
			r, err := q.GetRuntimeEnrollment(ctx, runtimeTokenDigest(token))
			if err != nil || r.ConsumedAt.Valid || !r.ExpiresAt.Time.After(time.Now()) || r.InstallationID != d.InstallationID {
				return ErrRuntimeNodeCredential
			}
			if d.Maintenance {
				return ErrSandboxDeploymentConflict
			}
		} else {
			id, err := parseConnectionGeneration(nodeID)
			if err != nil {
				return ErrRuntimeNodeCredential
			}
			n, err := q.GetRuntimeNode(ctx, id)
			if err != nil || n.InstallationID != d.InstallationID || n.CredentialSha256 != runtimeTokenDigest(token) {
				return ErrRuntimeNodeCredential
			}
			if n.DeploymentGeneration != d.Generation || n.SpecificationDigest != spec.Digest(d.ProviderKind) {
				return ErrRuntimeSpecificationMismatch
			}
		}
		result = RuntimeNodeConfiguration{InstallationID: runtimeUUID(d.InstallationID), Provider: d.ProviderKind, CoreURL: d.CoreUrl, Generation: uint64(d.Generation), Specification: spec, SpecificationDigest: spec.Digest(d.ProviderKind)}
		return nil
	})
	return result, err
}
