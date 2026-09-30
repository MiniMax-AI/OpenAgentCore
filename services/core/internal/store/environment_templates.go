package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentnetwork"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"time"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EnvironmentTemplate is configuration ownership, independent of provider images.

type EnvironmentTemplate struct {
	Plugins               []agentplugin.Metadata
	CapabilityDirectories []string
	Skills                []environmentconfig.SkillMetadata
	Packages              v1.EnvironmentPackages
	Initialization        environmentconfig.Setup
	Files                 []environmentconfig.InitialFileMetadata
	ID                    string
	Name                  *string
	NetworkAccess         string
	AllowedDomains        []string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type EnvironmentTemplateInput struct {
	Initialization                                                       environmentconfig.Setup
	SetEnv, SetSetup, SetPackages, SetSkills, SetPlugins, SetDirectories bool
	Files                                                                []environmentconfig.InitialFile
	SetFiles                                                             bool
	Name                                                                 *string
	SetName                                                              bool
	NetworkAccess                                                        string
	AllowedDomains                                                       []string
	SetNetwork                                                           bool
}

func (in EnvironmentTemplateInput) valid() bool {
	return in.Initialization.Validate() == nil && (in.Name == nil || (utf8.ValidString(*in.Name) && utf8.RuneCountInString(*in.Name) >= 1 && utf8.RuneCountInString(*in.Name) <= 256)) &&
		(!in.SetNetwork || (agentnetwork.Policy{Access: in.NetworkAccess, AllowedDomains: in.AllowedDomains}).Validate() == nil)
}

type templateMetadataRow sqlc.GetEnvironmentTemplateRow

func templateFromRow(row templateMetadataRow, err error) (EnvironmentTemplate, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentTemplate{}, ErrNotFound
	}
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	result := EnvironmentTemplate{CapabilityDirectories: append([]string{}, row.CapabilityDirectories...), ID: uuid.UUID(row.ID.Bytes).String(), NetworkAccess: row.NetworkAccess, AllowedDomains: append([]string{}, row.NetworkAllowedDomains...), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	if row.Name.Valid {
		result.Name = &row.Name.String
	}
	if json.Unmarshal(row.Files, &result.Files) != nil || environmentconfig.Decode(row.Packages, &result.Packages) != nil || json.Unmarshal(row.Skills, &result.Skills) != nil || json.Unmarshal(row.Plugins, &result.Plugins) != nil {
		return EnvironmentTemplate{}, ErrInvalidInput
	}
	return result, nil
}

func (s *Store) CreateEnvironmentTemplate(ctx context.Context, tenantID string, in EnvironmentTemplateInput) (EnvironmentTemplate, error) {
	if !in.SetNetwork {
		in.NetworkAccess = "enabled"
		in.AllowedDomains = nil
		in.SetNetwork = true
	}
	if !in.valid() {
		return EnvironmentTemplate{}, ErrInvalidInput
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	var name pgtype.Text
	if in.Name != nil {
		name = pgtype.Text{String: *in.Name, Valid: true}
	}
	id := uuid.New()
	metadata, encrypted, err := s.sealTemplateFiles(uuid.UUID(tenant.Bytes).String(), id.String(), in.Files)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	packages, envContents, setupContents, err := s.sealTemplateSetup(uuid.UUID(tenant.Bytes).String(), id.String(), in.Initialization)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	skills, skillContents, err := s.sealTemplateSkills(uuid.UUID(tenant.Bytes).String(), id.String(), in.Initialization)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	plugins, pluginContents, err := s.sealTemplatePlugins(uuid.UUID(tenant.Bytes).String(), id.String(), in.Initialization)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	var result EnvironmentTemplate
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.CreateEnvironmentTemplate(ctx, sqlc.CreateEnvironmentTemplateParams{ID: pgtype.UUID{Bytes: id, Valid: true}, TenantID: tenant, Name: name, NetworkAccess: in.NetworkAccess, NetworkAllowedDomains: append([]string{}, in.AllowedDomains...), Files: metadata, FileContents: encrypted, Packages: packages, EnvContents: envContents, SetupContents: setupContents, Skills: skills, SkillContents: skillContents, Plugins: plugins, PluginContents: pluginContents, CapabilityDirectories: append([]string{}, in.Initialization.CapabilityDirectories...)})
		result, err = templateFromRow(templateMetadataRow(row), err)
		if err != nil {
			return err
		}
		return recordWriteAudit(ctx, q, tenantID, "create", "environment_template", result.ID, "", AuditResource{Type: "environment_template", ID: result.ID})
	})
	return result, err
}

func (s *Store) GetEnvironmentTemplate(ctx context.Context, tenantID, templateID string) (EnvironmentTemplate, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	id, err := parseID(templateID)
	if err != nil {
		return EnvironmentTemplate{}, ErrNotFound
	}
	row, err := s.queries.GetEnvironmentTemplate(ctx, sqlc.GetEnvironmentTemplateParams{TenantID: tenant, ID: id})
	return templateFromRow(templateMetadataRow(row), err)
}

// Each supplied field replaces atomically, preserving concurrent unrelated updates.
func (s *Store) UpdateEnvironmentTemplate(ctx context.Context, tenantID, templateID string, in EnvironmentTemplateInput) (EnvironmentTemplate, error) {
	if !in.valid() {
		return EnvironmentTemplate{}, ErrInvalidInput
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	// Sealing runs before the update lookup, so a malformed ID must take the same path.
	id := parsePathID(templateID)
	var name pgtype.Text
	if in.Name != nil {
		name = pgtype.Text{String: *in.Name, Valid: true}
	}
	metadata, encrypted, err := s.sealTemplateFiles(uuid.UUID(tenant.Bytes).String(), uuid.UUID(id.Bytes).String(), in.Files)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	packages, envContents, setupContents, err := s.sealTemplateSetup(uuid.UUID(tenant.Bytes).String(), uuid.UUID(id.Bytes).String(), in.Initialization)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	skills, skillContents, err := s.sealTemplateSkills(uuid.UUID(tenant.Bytes).String(), uuid.UUID(id.Bytes).String(), in.Initialization)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	plugins, pluginContents, err := s.sealTemplatePlugins(uuid.UUID(tenant.Bytes).String(), uuid.UUID(id.Bytes).String(), in.Initialization)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	var result EnvironmentTemplate
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.UpdateEnvironmentTemplate(ctx, sqlc.UpdateEnvironmentTemplateParams{TenantID: tenant, ID: id, Name: name, SetName: in.SetName, NetworkAccess: in.NetworkAccess, NetworkAllowedDomains: append([]string{}, in.AllowedDomains...), SetNetwork: in.SetNetwork, SetFiles: in.SetFiles, Files: metadata, FileContents: encrypted, Packages: packages, EnvContents: envContents, SetupContents: setupContents, SetPackages: in.SetPackages, SetEnv: in.SetEnv, SetSetup: in.SetSetup, SetSkills: in.SetSkills, SetPlugins: in.SetPlugins, SetDirectories: in.SetDirectories, Skills: skills, SkillContents: skillContents, Plugins: plugins, PluginContents: pluginContents, CapabilityDirectories: append([]string{}, in.Initialization.CapabilityDirectories...)})
		result, err = templateFromRow(templateMetadataRow(row), err)
		if err != nil {
			return err
		}
		return recordWriteAudit(ctx, q, tenantID, "update", "environment_template", result.ID, "")
	})
	return result, err
}

func (s *Store) DeleteEnvironmentTemplate(ctx context.Context, tenantID, templateID string) (string, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return "", err
	}
	id, err := parseID(templateID)
	if err != nil {
		return "", ErrNotFound
	}
	var deletedID string
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		result, err := q.DeleteEnvironmentTemplate(ctx, sqlc.DeleteEnvironmentTemplateParams{TenantID: tenant, ID: id})
		if err != nil {
			return err
		}
		deletedID = uuid.UUID(result.Bytes).String()
		return recordWriteAudit(ctx, q, tenantID, "delete", "environment_template", deletedID, "")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return deletedID, nil
}

type EnvironmentTemplatePage struct {
	Templates []EnvironmentTemplate
	HasMore   bool
}

func (s *Store) ListEnvironmentTemplates(ctx context.Context, tenantID, cursor string, limit int, ascending bool) (EnvironmentTemplatePage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return EnvironmentTemplatePage{}, err
	}
	if limit < 1 || limit > 100 {
		return EnvironmentTemplatePage{}, ErrInvalidInput
	}
	params := sqlc.ListEnvironmentTemplatesParams{TenantID: tenant, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if cursor != "" {
		after, err := s.GetEnvironmentTemplate(ctx, tenantID, lookupCursor(cursor))
		if err != nil {
			return EnvironmentTemplatePage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListEnvironmentTemplates(ctx, params)
	if err != nil {
		return EnvironmentTemplatePage{}, err
	}
	page := EnvironmentTemplatePage{Templates: make([]EnvironmentTemplate, 0, min(limit, len(rows))), HasMore: len(rows) > limit}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	for _, row := range rows {
		value, err := templateFromRow(templateMetadataRow(row), nil)
		if err != nil {
			return EnvironmentTemplatePage{}, err
		}
		page.Templates = append(page.Templates, value)
	}
	return page, nil
}
