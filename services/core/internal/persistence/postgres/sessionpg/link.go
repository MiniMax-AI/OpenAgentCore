package sessionpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// attachGrantPurpose is the credential key purpose attach grants are signed
// under.
const attachGrantPurpose = "sandbox-link-attach-grant"

// GetServeAuthority reads the live Link resource with the ID. A malformed or
// unknown resource has none.
func (s *Store) GetServeAuthority(ctx context.Context, id string) (runtimedevice.ServeAuthority, bool, error) {
	resource, err := pgunit.ParseID(id)
	if err != nil {
		return runtimedevice.ServeAuthority{}, false, nil
	}
	row, err := s.units.Queries().GetSandboxServeAuthority(ctx, resource)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimedevice.ServeAuthority{}, false, nil
	}
	if err != nil {
		return runtimedevice.ServeAuthority{}, false, err
	}
	return runtimedevice.ServeAuthority{
		Resource:       linkResource(row.TenantID, row.EnvironmentID, pgtype.Text{String: row.Kind, Valid: true}, resource, pgtype.Int8{Int64: row.Generation, Valid: true}),
		CredentialHash: row.CredentialHash.String,
	}, true, nil
}

func (s *Store) ListLiveSandboxResources(ctx context.Context) ([]sessions.SandboxResource, error) {
	rows, err := s.units.Queries().ListLiveSandboxResources(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]sessions.SandboxResource, 0, len(rows))
	for _, row := range rows {
		result = append(result, sessions.SandboxResource{
			Resource: linkResource(row.TenantID, row.EnvironmentID, pgtype.Text{String: row.Kind, Valid: true}, row.ID, pgtype.Int8{Int64: row.Generation, Valid: true}),
			Quiesced: row.Quiesced,
		})
	}
	return result, nil
}

// GetEnvironmentResource reads the live Link resource of the tenant's
// Environment; without one, or for a malformed ID, it is ErrNotFound.
func (s *Store) GetEnvironmentResource(ctx context.Context, tenant, environment string) (runtimedevice.ServeAuthority, error) {
	lookup, err := ResourceLookup(tenant, environment)
	if err != nil {
		return runtimedevice.ServeAuthority{}, sessions.ErrNotFound
	}
	row, err := s.units.Queries().GetEnvironmentResource(ctx, sqlc.GetEnvironmentResourceParams{TenantID: lookup.TenantID, EnvironmentID: lookup.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimedevice.ServeAuthority{}, sessions.ErrNotFound
	}
	if err != nil {
		return runtimedevice.ServeAuthority{}, err
	}
	return runtimedevice.ServeAuthority{
		Resource:       linkResource(lookup.TenantID, lookup.ID, pgtype.Text{String: row.Kind, Valid: true}, row.ID, pgtype.Int8{Int64: row.Generation, Valid: true}),
		CredentialHash: row.CredentialHash.String,
	}, nil
}

// GetAgentHostCredential reads a live agent host's credential. A malformed or
// unknown device, or one that is not an agent host, has none.
func (s *Store) GetAgentHostCredential(ctx context.Context, runtime string) (runtimedevice.AgentHost, bool, error) {
	id, err := pgunit.ParseID(runtime)
	if err != nil {
		return runtimedevice.AgentHost{}, false, nil
	}
	row, err := s.units.Queries().GetAgentHostCredential(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimedevice.AgentHost{}, false, nil
	}
	if err != nil {
		return runtimedevice.AgentHost{}, false, err
	}
	return runtimedevice.AgentHost{CredentialHash: row.CredentialHash, Revision: uint64(row.CredentialRevision)}, true, nil
}

// RegisterAgentHost records the deployment's agent host with the digest of
// its credential. Registering it again with the same credential changes
// nothing.
func (s *Store) RegisterAgentHost(ctx context.Context, runtime, credentialHash string) error {
	id, err := pgunit.ParseID(runtime)
	if err != nil {
		return err
	}
	_, err = s.units.Queries().RegisterAgentHost(ctx, sqlc.RegisterAgentHostParams{ID: id, CredentialHash: pgtype.Text{String: credentialHash, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("agent host %s is registered as another device", runtime)
	}
	return err
}

// GetLinkAssignment reads an assignment as the Link authority sees it. A
// malformed or unknown assignment ID has none.
func (s *Store) GetLinkAssignment(ctx context.Context, assignment string) (runtimedevice.LinkAssignment, bool, error) {
	id, err := pgunit.ParseID(assignment)
	if err != nil {
		return runtimedevice.LinkAssignment{}, false, nil
	}
	row, err := s.units.Queries().GetLinkAssignment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimedevice.LinkAssignment{}, false, nil
	}
	if err != nil {
		return runtimedevice.LinkAssignment{}, false, err
	}
	return runtimedevice.LinkAssignment{
		SessionID: optionalID(row.SessionID), RuntimeID: optionalID(row.RuntimeID), Epoch: uint64(row.Epoch), Bound: row.Bound,
		AgentHost: row.AgentHost, Revision: uint64(row.CredentialRevision), NetworkEnabled: row.NetworkEnabled,
		Resource: linkResource(row.ResourceTenantID, row.ResourceEnvironmentID, row.ResourceKind, row.ResourceID, row.ResourceGeneration),
	}, true, nil
}

// SignAttachGrant returns the keyed digest of an attach grant's payload.
func (s *Store) SignAttachGrant(_ context.Context, payload string) (string, error) {
	return s.cipher.Fingerprint(attachGrantPurpose, payload)
}

// linkResource returns a resource of the sandbox_resources view, or the zero
// Resource when the row has none.
func linkResource(tenant, environment pgtype.UUID, kind pgtype.Text, id pgtype.UUID, generation pgtype.Int8) sandboxbootstrap.Resource {
	if !kind.Valid {
		return sandboxbootstrap.Resource{}
	}
	return sandboxbootstrap.Resource{TenantID: optionalID(tenant), EnvironmentID: optionalID(environment), Kind: kind.String, ID: optionalID(id), Generation: uint64(generation.Int64)}
}
