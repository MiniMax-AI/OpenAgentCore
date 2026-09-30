package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) sealTemplateFiles(tenant, id string, files []environmentconfig.InitialFile) ([]byte, []byte, error) {
	if environmentconfig.ValidateInitialFiles(files) != nil {
		return nil, nil, ErrInvalidInput
	}
	metadata, err := json.Marshal(environmentconfig.InitialFilesMetadata(files))
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return metadata, nil, nil
	}
	input, err := json.Marshal(files)
	if err != nil {
		return nil, nil, err
	}
	encrypted, err := s.credentialCipher.SealEnvironmentFile(input, credentialcrypto.EnvironmentFileBinding{TenantID: tenant, Resource: "environment_template", OwnerID: id, FileID: "files"})
	return metadata, encrypted, err
}

// ResolveEnvironmentTemplate reads one atomic snapshot; public reads need no decryption key.
func (s *Store) ResolveEnvironmentTemplate(ctx context.Context, tenant, id string) (EnvironmentTemplate, []environmentconfig.InitialFile, error) {
	return s.resolveEnvironmentTemplate(ctx, s.queries, tenant, id)
}

func (s *Store) resolveEnvironmentTemplate(ctx context.Context, q *sqlc.Queries, tenant, id string) (EnvironmentTemplate, []environmentconfig.InitialFile, error) {
	lookup, err := deviceLookup(tenant, id)
	if err != nil {
		return EnvironmentTemplate{}, nil, ErrNotFound
	}
	row, err := q.ResolveEnvironmentTemplate(ctx, sqlc.ResolveEnvironmentTemplateParams{TenantID: lookup.TenantID, ID: lookup.ID})
	value, err := templateFromRow(templateMetadataRow{ID: row.ID, TenantID: row.TenantID, Name: row.Name, NetworkAccess: row.NetworkAccess, NetworkAllowedDomains: row.NetworkAllowedDomains, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Files: row.Files, Packages: row.Packages, Skills: row.Skills, Plugins: row.Plugins, CapabilityDirectories: row.CapabilityDirectories}, err)
	if err != nil {
		return value, nil, err
	}
	value.Initialization.Packages = value.Packages
	canonicalTenant := uuid.UUID(lookup.TenantID.Bytes).String()
	if err = s.openEnvironmentSetup(canonicalTenant, "environment_template", value.ID, "env", row.EnvContents, &value.Initialization.Env); err != nil {
		return value, nil, err
	}
	if err = s.openEnvironmentSetup(canonicalTenant, "environment_template", value.ID, "setup_commands", row.SetupContents, &value.Initialization.Commands); err != nil {
		return value, nil, err
	}
	if err = s.openEnvironmentSetup(canonicalTenant, "environment_template", value.ID, "skills", row.SkillContents, &value.Initialization.Skills); err != nil {
		return value, nil, err
	}
	if len(value.Skills) != len(value.Initialization.Skills) {
		return value, nil, ErrInvalidInput
	}
	for i, metadata := range value.Skills {
		if metadata != value.Initialization.Skills[i].Metadata {
			return value, nil, ErrInvalidInput
		}
	}
	value.Initialization.CapabilityDirectories = append([]string(nil), value.CapabilityDirectories...)
	if err = s.openEnvironmentSetup(canonicalTenant, "environment_template", value.ID, "plugins", row.PluginContents, &value.Initialization.Plugins); err != nil {
		return value, nil, err
	}
	if len(value.Plugins) != len(value.Initialization.Plugins) {
		return value, nil, ErrInvalidInput
	}
	for i, metadata := range value.Plugins {
		if metadata != value.Initialization.Plugins[i].Metadata {
			return value, nil, ErrInvalidInput
		}
	}
	if value.Initialization.Validate() != nil {
		return value, nil, ErrInvalidInput
	}
	if len(row.FileContents) == 0 {
		if len(value.Files) > 0 {
			return value, nil, ErrInvalidInput
		}
		return value, nil, nil
	}
	plain, err := s.credentialCipher.OpenEnvironmentFile(row.FileContents, credentialcrypto.EnvironmentFileBinding{TenantID: uuid.UUID(lookup.TenantID.Bytes).String(), Resource: "environment_template", OwnerID: value.ID, FileID: "files"})
	if err != nil {
		return value, nil, err
	}
	var files []environmentconfig.InitialFile
	if json.Unmarshal(plain, &files) != nil || environmentconfig.ValidateInitialFiles(files) != nil {
		return value, nil, ErrInvalidInput
	}
	return value, files, nil
}

func (s *Store) saveInitialFiles(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, tenant string, session pgtype.UUID, files []environmentconfig.InitialFile) ([]byte, error) {
	if environmentconfig.ValidateInitialFiles(files) != nil {
		return nil, ErrInvalidInput
	}
	tenantID, err := parseID(tenant)
	if err != nil {
		return nil, err
	}
	tenant = uuid.UUID(tenantID.Bytes).String()
	metadata := environmentconfig.InitialFilesMetadata(files)
	for i, f := range files {
		body := f.Data
		if f.Type == "file_id" {
			sourceTenant, sourceID, err := sourceFileIDs(tenant, f.FileID)
			if err != nil {
				return nil, err
			}
			source, err := q.LockInitialSourceFile(ctx, sqlc.LockInitialSourceFileParams{TenantID: sourceTenant, ID: sourceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			err = consumeSourceFile(ctx, tx, source, func(source SourceFile, reader io.Reader) error {
				if source.SizeBytes > environmentconfig.MaxInitialFileBytes {
					return ErrInvalidInput
				}
				var err error
				body, err = io.ReadAll(io.LimitReader(reader, environmentconfig.MaxInitialFileBytes+1))
				if err == nil && (len(body) > environmentconfig.MaxInitialFileBytes || int64(len(body)) != source.SizeBytes) {
					return ErrInvalidInput
				}
				return err
			})
			if err != nil {
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

// ReadInitialEnvironmentFile decrypts only the next frozen file, bounding memory per installation.
func (s *Store) ReadInitialEnvironmentFile(ctx context.Context, tenant, session string, position int) (environmentconfig.InitialFileMetadata, []byte, error) {
	lookup, err := deviceLookup(tenant, session)
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
		err = ErrInvalidInput
	}
	return environmentconfig.InitialFileMetadata{ID: id, Path: row.Path, SizeBytes: &row.SizeBytes}, body, err
}
