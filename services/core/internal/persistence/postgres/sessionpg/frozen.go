package sessionpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// ReadEnvironmentSetup opens the setup the Session froze at creation. Frozen
// data that does not open, decode or validate is corrupt stored data, never
// the caller's input, so it is an internal error.
func (s *Store) ReadEnvironmentSetup(ctx context.Context, tenant, session string) (environmentconfig.Setup, error) {
	lookup, err := DeviceLookup(tenant, session)
	if err != nil {
		return environmentconfig.Setup{}, sessions.ErrNotFound
	}
	encrypted, err := s.units.Queries().GetEnvironmentSetup(ctx, sqlc.GetEnvironmentSetupParams{TenantID: lookup.TenantID, ID: lookup.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return environmentconfig.Setup{}, sessions.ErrNotFound
	}
	if err != nil {
		return environmentconfig.Setup{}, err
	}
	var setup environmentconfig.Setup
	if len(encrypted) == 0 {
		return setup, nil
	}
	if s.cipher == nil {
		return environmentconfig.Setup{}, credentialcrypto.ErrUnavailable
	}
	binding := credentialcrypto.EnvironmentSetupBinding{TenantID: uuid.UUID(lookup.TenantID.Bytes).String(), Resource: "session", OwnerID: uuid.UUID(lookup.ID.Bytes).String(), Field: "initialization"}
	plaintext, err := s.cipher.OpenEnvironmentSetup(encrypted, binding)
	if err != nil {
		return environmentconfig.Setup{}, fmt.Errorf("open frozen environment setup: %w", err)
	}
	if environmentconfig.Decode(plaintext, &setup) != nil || setup.ValidateInstalled() != nil {
		return environmentconfig.Setup{}, errors.New("frozen environment setup is invalid")
	}
	return setup, nil
}

// ReadInitialEnvironmentFile opens the initial file the Session froze at
// position. A file that does not open or match its recorded size is corrupt
// stored data, so it is an internal error.
func (s *Store) ReadInitialEnvironmentFile(ctx context.Context, tenant, session string, position int) (environmentconfig.InitialFileMetadata, []byte, error) {
	lookup, err := DeviceLookup(tenant, session)
	if err != nil {
		return environmentconfig.InitialFileMetadata{}, nil, err
	}
	row, err := s.units.Queries().GetInitialEnvironmentFile(ctx, sqlc.GetInitialEnvironmentFileParams{TenantID: lookup.TenantID, SessionID: lookup.ID, Position: int32(position)})
	if errors.Is(err, pgx.ErrNoRows) {
		return environmentconfig.InitialFileMetadata{}, nil, sessions.ErrNotFound
	}
	if err != nil {
		return environmentconfig.InitialFileMetadata{}, nil, err
	}
	if s.cipher == nil {
		return environmentconfig.InitialFileMetadata{}, nil, credentialcrypto.ErrUnavailable
	}
	id := uuid.UUID(row.ID.Bytes).String()
	binding := credentialcrypto.EnvironmentFileBinding{TenantID: uuid.UUID(lookup.TenantID.Bytes).String(), Resource: "session", OwnerID: uuid.UUID(lookup.ID.Bytes).String(), FileID: id}
	body, err := s.cipher.OpenEnvironmentFile(row.Contents, binding)
	if err != nil {
		return environmentconfig.InitialFileMetadata{}, nil, fmt.Errorf("open frozen initial file: %w", err)
	}
	if int64(len(body)) != row.SizeBytes {
		return environmentconfig.InitialFileMetadata{}, nil, errors.New("frozen initial file does not match its recorded size")
	}
	return environmentconfig.InitialFileMetadata{ID: id, Path: row.Path, SizeBytes: &row.SizeBytes}, body, nil
}
