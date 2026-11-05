package sessionpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// ReadEnvironmentSetup opens the setup frozen for the Environment. A reclaimed
// Environment, whose owning Session was deleted, is sessions.ErrNotFound.
// Frozen data that does not open, decode or validate is corrupt stored data,
// never the caller's input, so it is an internal error.
func (s *Store) ReadEnvironmentSetup(ctx context.Context, tenant, environment string) (environmentconfig.Setup, error) {
	lookup, err := ResourceLookup(tenant, environment)
	if err != nil {
		return environmentconfig.Setup{}, sessions.ErrNotFound
	}
	row, err := s.units.Queries().GetEnvironmentSetup(ctx, sqlc.GetEnvironmentSetupParams{TenantID: lookup.TenantID, ID: lookup.ID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Reclaimed) {
		return environmentconfig.Setup{}, sessions.ErrNotFound
	}
	if err != nil {
		return environmentconfig.Setup{}, err
	}
	var setup environmentconfig.Setup
	if len(row.Contents) == 0 {
		return setup, nil
	}
	plaintext, err := s.cipher.OpenEnvironmentSetup(row.Contents, setupBinding(lookup.TenantID, lookup.ID))
	if err != nil {
		return environmentconfig.Setup{}, fmt.Errorf("open frozen environment setup: %w", err)
	}
	if environmentconfig.Decode(plaintext, &setup) != nil || setup.ValidateInstalled() != nil {
		return environmentconfig.Setup{}, errors.New("frozen environment setup is invalid")
	}
	return setup, nil
}

// ReadInitialEnvironmentFile opens the initial file frozen for the
// Environment at position. A file that does not open or match its recorded size is corrupt
// stored data, so it is an internal error.
func (s *Store) ReadInitialEnvironmentFile(ctx context.Context, tenant, environment string, position int) (environmentconfig.InitialFileMetadata, []byte, error) {
	lookup, err := ResourceLookup(tenant, environment)
	if err != nil {
		return environmentconfig.InitialFileMetadata{}, nil, err
	}
	row, err := s.units.Queries().GetInitialEnvironmentFile(ctx, sqlc.GetInitialEnvironmentFileParams{TenantID: lookup.TenantID, EnvironmentID: lookup.ID, Position: int32(position)})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Reclaimed) {
		return environmentconfig.InitialFileMetadata{}, nil, sessions.ErrNotFound
	}
	if err != nil {
		return environmentconfig.InitialFileMetadata{}, nil, err
	}
	id := uuid.UUID(row.ID.Bytes).String()
	body, err := s.cipher.OpenEnvironmentFile(row.Contents, fileBinding(lookup.TenantID, lookup.ID, id))
	if err != nil {
		return environmentconfig.InitialFileMetadata{}, nil, fmt.Errorf("open frozen initial file: %w", err)
	}
	if int64(len(body)) != row.SizeBytes {
		return environmentconfig.InitialFileMetadata{}, nil, errors.New("frozen initial file does not match its recorded size")
	}
	return environmentconfig.InitialFileMetadata{ID: id, Path: row.Path, SizeBytes: &row.SizeBytes}, body, nil
}

// setupBinding binds the setup frozen for an Environment to the tenant and
// the Environment.
func setupBinding(tenant, environment pgtype.UUID) credentialcrypto.EnvironmentSetupBinding {
	return credentialcrypto.EnvironmentSetupBinding{TenantID: optionalID(tenant), Resource: "environment", OwnerID: optionalID(environment), Field: "initialization"}
}

// fileBinding binds an initial file frozen for an Environment to the tenant,
// the Environment and the file.
func fileBinding(tenant, environment pgtype.UUID, file string) credentialcrypto.EnvironmentFileBinding {
	return credentialcrypto.EnvironmentFileBinding{TenantID: optionalID(tenant), Resource: "environment", OwnerID: optionalID(environment), FileID: file}
}
