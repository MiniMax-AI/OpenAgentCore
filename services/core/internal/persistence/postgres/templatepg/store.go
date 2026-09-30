// Package templatepg stores Environment Templates in PostgreSQL. Confidential
// configuration (env, setup commands, Skill and Plugin archives and initial
// file contents) is sealed with the credential key, each field bound to its
// tenant, Template and field; safe metadata is stored beside it in plaintext.
package templatepg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// resource names Templates in credential bindings and audit rows.
const resource = "environment_template"

// Store implements environmenttemplates.Storage and environmenttemplates.Reader.
type Store struct {
	pool   *pgunit.Pool
	cipher *credentialcrypto.Cipher
}

var (
	_ environmenttemplates.Storage = (*Store)(nil)
	_ environmenttemplates.Reader  = (*Store)(nil)
)

// New returns a Store. Without a credential key (cipher nil), writes and
// resolutions that seal or open confidential configuration fail with
// credentialcrypto.ErrUnavailable; safe metadata stays readable.
func New(pool *pgunit.Pool, cipher *credentialcrypto.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

func (s *Store) Create(ctx context.Context, tenantID string, in environmenttemplates.Input) (environmenttemplates.Template, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return environmenttemplates.Template{}, environmenttemplates.ErrInvalidInput
	}
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	sealed, err := s.seal(tenant, id, in)
	if err != nil {
		return environmenttemplates.Template{}, err
	}
	var result environmenttemplates.Template
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.CreateEnvironmentTemplate(ctx, sqlc.CreateEnvironmentTemplateParams{ID: id, TenantID: tenant, Name: name(in.Name), NetworkAccess: in.NetworkAccess, NetworkAllowedDomains: append([]string{}, in.AllowedDomains...), Files: sealed.files, FileContents: sealed.fileContents, Packages: sealed.packages, EnvContents: sealed.env, SetupContents: sealed.commands, Skills: sealed.skills, SkillContents: sealed.skillContents, Plugins: sealed.plugins, PluginContents: sealed.pluginContents, CapabilityDirectories: append([]string{}, in.Setup.CapabilityDirectories...)})
		if err != nil {
			return err
		}
		if result, err = template(metadataRow(row)); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "create", resource, result.ID, "", writeaudit.Resource{Type: resource, ID: result.ID})
	})
	return result, storageError(err)
}

// Update seals before it looks the Template up, so a malformed ID takes the
// path of a missing one. Each supplied field replaces atomically in one
// statement, so concurrent updates of other fields are kept.
func (s *Store) Update(ctx context.Context, tenantID, templateID string, in environmenttemplates.Input) (environmenttemplates.Template, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return environmenttemplates.Template{}, environmenttemplates.ErrInvalidInput
	}
	id := pgunit.PathID(templateID)
	sealed, err := s.seal(tenant, id, in)
	if err != nil {
		return environmenttemplates.Template{}, err
	}
	var result environmenttemplates.Template
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.UpdateEnvironmentTemplate(ctx, sqlc.UpdateEnvironmentTemplateParams{TenantID: tenant, ID: id, Name: name(in.Name), SetName: in.SetName, NetworkAccess: in.NetworkAccess, NetworkAllowedDomains: append([]string{}, in.AllowedDomains...), SetNetwork: in.SetNetwork, SetFiles: in.SetFiles, Files: sealed.files, FileContents: sealed.fileContents, Packages: sealed.packages, EnvContents: sealed.env, SetupContents: sealed.commands, SetPackages: in.SetPackages, SetEnv: in.SetEnv, SetSetup: in.SetCommands, SetSkills: in.SetSkills, SetPlugins: in.SetPlugins, SetDirectories: in.SetDirectories, Skills: sealed.skills, SkillContents: sealed.skillContents, Plugins: sealed.plugins, PluginContents: sealed.pluginContents, CapabilityDirectories: append([]string{}, in.Setup.CapabilityDirectories...)})
		if err != nil {
			return err
		}
		if result, err = template(metadataRow(row)); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "update", resource, result.ID, "")
	})
	return result, storageError(err)
}

func (s *Store) Delete(ctx context.Context, tenantID, templateID string) (string, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return "", environmenttemplates.ErrInvalidInput
	}
	var deleted string
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		id, err := q.DeleteEnvironmentTemplate(ctx, sqlc.DeleteEnvironmentTemplateParams{TenantID: tenant, ID: pgunit.PathID(templateID)})
		if err != nil {
			return err
		}
		deleted = uuid.UUID(id.Bytes).String()
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "delete", resource, deleted, "")
	})
	if err != nil {
		return "", storageError(err)
	}
	return deleted, nil
}

func (s *Store) Get(ctx context.Context, tenantID, templateID string) (environmenttemplates.Template, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return environmenttemplates.Template{}, environmenttemplates.ErrInvalidInput
	}
	row, err := s.pool.Queries().GetEnvironmentTemplate(ctx, sqlc.GetEnvironmentTemplateParams{TenantID: tenant, ID: pgunit.PathID(templateID)})
	if err != nil {
		return environmenttemplates.Template{}, storageError(err)
	}
	return template(metadataRow(row))
}

// List reads the cursor Template and the page in one snapshot. A cursor that
// names no Template of the tenant returns ErrNotFound.
func (s *Store) List(ctx context.Context, tenantID string, query environmenttemplates.ListQuery) (environmenttemplates.Page, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return environmenttemplates.Page{}, environmenttemplates.ErrInvalidInput
	}
	if err := query.Validate(); err != nil {
		return environmenttemplates.Page{}, err
	}
	params := sqlc.ListEnvironmentTemplatesParams{TenantID: tenant, PageLimit: int32(query.Limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: query.Ascending}
	var rows []sqlc.ListEnvironmentTemplatesRow
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if query.After != "" {
			after, err := q.GetEnvironmentTemplate(ctx, sqlc.GetEnvironmentTemplateParams{TenantID: tenant, ID: pgunit.PathID(pgunit.LookupCursor(query.After))})
			if err != nil {
				return err
			}
			params.AfterCreated, params.AfterID = after.CreatedAt, after.ID
		}
		var err error
		rows, err = q.ListEnvironmentTemplates(ctx, params)
		return err
	})
	if err != nil {
		return environmenttemplates.Page{}, storageError(err)
	}
	page := environmenttemplates.Page{Templates: make([]environmenttemplates.Template, 0, min(query.Limit, len(rows))), HasMore: len(rows) > query.Limit}
	for _, row := range rows[:min(query.Limit, len(rows))] {
		value, err := template(metadataRow(row))
		if err != nil {
			return environmenttemplates.Page{}, err
		}
		page.Templates = append(page.Templates, value)
	}
	return page, nil
}

// Resolve reads one row, so the metadata and every confidential field come
// from the same committed version. Stored Skill and Plugin metadata must match
// the sealed archives they describe. Any malformed ID resolves to ErrNotFound.
func (s *Store) Resolve(ctx context.Context, tenantID, templateID string) (environmenttemplates.Resolved, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return environmenttemplates.Resolved{}, environmenttemplates.ErrNotFound
	}
	id, err := pgunit.ParseID(templateID)
	if err != nil {
		return environmenttemplates.Resolved{}, environmenttemplates.ErrNotFound
	}
	row, err := s.pool.Queries().ResolveEnvironmentTemplate(ctx, sqlc.ResolveEnvironmentTemplateParams{TenantID: tenant, ID: id})
	if err != nil {
		return environmenttemplates.Resolved{}, storageError(err)
	}
	metadata, err := template(metadataRow{ID: row.ID, TenantID: row.TenantID, Name: row.Name, NetworkAccess: row.NetworkAccess, NetworkAllowedDomains: row.NetworkAllowedDomains, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Files: row.Files, Packages: row.Packages, Skills: row.Skills, Plugins: row.Plugins, CapabilityDirectories: row.CapabilityDirectories})
	if err != nil {
		return environmenttemplates.Resolved{}, err
	}
	resolved := environmenttemplates.Resolved{Template: metadata, Setup: environmentconfig.Setup{Packages: metadata.Packages, CapabilityDirectories: append([]string(nil), metadata.CapabilityDirectories...)}}
	for _, field := range []struct {
		name       string
		ciphertext []byte
		output     any
	}{
		{"env", row.EnvContents, &resolved.Setup.Env},
		{"setup_commands", row.SetupContents, &resolved.Setup.Commands},
		{"skills", row.SkillContents, &resolved.Setup.Skills},
		{"plugins", row.PluginContents, &resolved.Setup.Plugins},
	} {
		if err := s.openSetup(tenant, id, field.name, field.ciphertext, field.output); err != nil {
			return environmenttemplates.Resolved{}, err
		}
	}
	if !matches(metadata.Skills, resolved.Setup.SkillMetadata()) || !matches(metadata.Plugins, resolved.Setup.PluginMetadata()) || resolved.Setup.Validate() != nil {
		return environmenttemplates.Resolved{}, environmenttemplates.ErrInvalidInput
	}
	if len(row.FileContents) == 0 {
		if len(metadata.Files) > 0 {
			return environmenttemplates.Resolved{}, environmenttemplates.ErrInvalidInput
		}
		return resolved, nil
	}
	if s.cipher == nil {
		return environmenttemplates.Resolved{}, credentialcrypto.ErrUnavailable
	}
	plaintext, err := s.cipher.OpenEnvironmentFile(row.FileContents, fileBinding(tenant, id))
	if err != nil {
		return environmenttemplates.Resolved{}, err
	}
	if json.Unmarshal(plaintext, &resolved.Files) != nil || environmentconfig.ValidateInitialFiles(resolved.Files) != nil {
		return environmenttemplates.Resolved{}, environmenttemplates.ErrInvalidInput
	}
	return resolved, nil
}

func matches[T comparable](stored, sealed []T) bool {
	if len(stored) != len(sealed) {
		return false
	}
	for i := range stored {
		if stored[i] != sealed[i] {
			return false
		}
	}
	return true
}

// metadataRow is the safe column set every Template query returns.
type metadataRow sqlc.GetEnvironmentTemplateRow

func template(row metadataRow) (environmenttemplates.Template, error) {
	result := environmenttemplates.Template{ID: uuid.UUID(row.ID.Bytes).String(), NetworkAccess: row.NetworkAccess, AllowedDomains: append([]string{}, row.NetworkAllowedDomains...), CapabilityDirectories: append([]string{}, row.CapabilityDirectories...), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	if row.Name.Valid {
		result.Name = &row.Name.String
	}
	if json.Unmarshal(row.Files, &result.Files) != nil || environmentconfig.Decode(row.Packages, &result.Packages) != nil || json.Unmarshal(row.Skills, &result.Skills) != nil || json.Unmarshal(row.Plugins, &result.Plugins) != nil {
		return environmenttemplates.Template{}, environmenttemplates.ErrInvalidInput
	}
	return result, nil
}

func name(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

// storageError translates the database outcomes callers act on: no row is
// ErrNotFound and text PostgreSQL cannot store is textvalue.ErrUnstorable.
// Every other error returns as is.
func storageError(err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return environmenttemplates.ErrNotFound
	case pgunit.IsUnstorableText(err):
		return textvalue.ErrUnstorable
	}
	return err
}
