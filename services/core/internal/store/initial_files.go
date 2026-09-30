package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) saveInitialFiles(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, tenant string, session pgtype.UUID, initial []environmentconfig.InitialFile) ([]byte, error) {
	if environmentconfig.ValidateInitialFiles(initial) != nil {
		return nil, sessions.ErrInvalidInput
	}
	tenantID, err := parseID(tenant)
	if err != nil {
		return nil, err
	}
	tenant = uuid.UUID(tenantID.Bytes).String()
	metadata := environmentconfig.InitialFilesMetadata(initial)
	for i, f := range initial {
		body := f.Data
		if f.Type == "file_id" {
			sourceID, ok := files.ParseID(f.FileID)
			if !ok {
				return nil, sessions.ErrNotFound
			}
			source, err := q.LockInitialSourceFile(ctx, sqlc.LockInitialSourceFileParams{TenantID: tenantID, ID: pgtype.UUID{Bytes: sourceID, Valid: true}})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, sessions.ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			if body, err = readInitialSourceFile(ctx, tx, source); err != nil {
				return nil, err
			}
		}
		id := uuid.NewString()
		size := int64(len(body))
		metadata[i].ID = id
		metadata[i].SizeBytes = &size
		encrypted, err := s.credentialCipher.SealEnvironmentFile(body, credentialcrypto.EnvironmentFileBinding{TenantID: tenant, Resource: "session", OwnerID: uuid.UUID(session.Bytes).String(), FileID: id})
		if err != nil {
			return nil, err
		}
		fileID, _ := parseID(id)
		if err := q.CreateInitialEnvironmentFile(ctx, sqlc.CreateInitialEnvironmentFileParams{ID: fileID, SessionID: session, Position: int32(i), Path: f.Path, SizeBytes: size, Contents: encrypted}); err != nil {
			return nil, err
		}
	}
	return json.Marshal(metadata)
}

// readInitialSourceFile copies a File's content for a new Session. Session
// creation reads source_files through store's SQL until Sessions move out of
// store.
func readInitialSourceFile(ctx context.Context, tx pgx.Tx, source sqlc.SourceFile) ([]byte, error) {
	if source.SizeBytes > environmentconfig.MaxInitialFileBytes {
		return nil, sessions.ErrInvalidInput
	}
	objects := tx.LargeObjects()
	reader, err := objects.Open(ctx, source.BodyOid.Uint32, pgx.LargeObjectModeRead)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(reader, environmentconfig.MaxInitialFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > environmentconfig.MaxInitialFileBytes || int64(len(body)) != source.SizeBytes {
		return nil, sessions.ErrInvalidInput
	}
	return body, reader.Close()
}

// ReadInitialEnvironmentFile decrypts only the next frozen file, bounding memory per installation.
func (s *Store) ReadInitialEnvironmentFile(ctx context.Context, tenant, session string, position int) (environmentconfig.InitialFileMetadata, []byte, error) {
	lookup, err := sessionpg.DeviceLookup(tenant, session)
	if err != nil {
		return environmentconfig.InitialFileMetadata{}, nil, err
	}
	row, err := s.queries.GetInitialEnvironmentFile(ctx, sqlc.GetInitialEnvironmentFileParams{TenantID: lookup.TenantID, SessionID: lookup.ID, Position: int32(position)})
	if err != nil {
		return environmentconfig.InitialFileMetadata{}, nil, err
	}
	id := uuid.UUID(row.ID.Bytes).String()
	body, err := s.credentialCipher.OpenEnvironmentFile(row.Contents, credentialcrypto.EnvironmentFileBinding{TenantID: uuid.UUID(lookup.TenantID.Bytes).String(), Resource: "session", OwnerID: uuid.UUID(lookup.ID.Bytes).String(), FileID: id})
	if err == nil && int64(len(body)) != row.SizeBytes {
		err = sessions.ErrInvalidInput
	}
	return environmentconfig.InitialFileMetadata{ID: id, Path: row.Path, SizeBytes: &row.SizeBytes}, body, err
}
