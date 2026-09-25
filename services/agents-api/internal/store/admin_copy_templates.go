package store

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func (c *assetCopier) template(ctx context.Context, id string) (string, error) {
	source, err := parseID(id)
	if err != nil {
		return "", ErrNotFound
	}
	if _, err := c.q.LockAdminCopyTemplate(ctx, sqlc.LockAdminCopyTemplateParams{TenantID: c.source, ID: source}); err != nil {
		return "", err
	}
	original, files, err := c.s.resolveEnvironmentTemplate(ctx, c.q, c.sourceTenant, id)
	if err != nil {
		return "", err
	}
	setup := original.Initialization
	for i := range setup.Skills {
		skill := &setup.Skills[i]
		if skill.Metadata.Type != "skill_reference" {
			continue
		}
		if !c.dependencies {
			return "", ErrInvalidInput
		}
		skill.Metadata.SkillID, err = c.copy(ctx, "skill", skill.Metadata.SkillID, "")
		if err != nil {
			return "", err
		}
	}
	for i := range files {
		if files[i].Type != "file_id" {
			continue
		}
		if !c.dependencies {
			return "", ErrInvalidInput
		}
		files[i].FileID, err = c.copy(ctx, "file", files[i].FileID, "")
		if err != nil {
			return "", err
		}
	}
	target := copyUUID()
	targetID := copyID(target)
	metadata, encrypted, err := c.s.sealTemplateFiles(c.targetTenant, targetID, files)
	if err != nil {
		return "", err
	}
	packages, env, commands, err := c.s.sealTemplateSetup(c.targetTenant, targetID, setup)
	if err != nil {
		return "", err
	}
	skills, skillContents, err := c.s.sealTemplateSkills(c.targetTenant, targetID, setup)
	if err != nil {
		return "", err
	}
	plugins, pluginContents, err := c.s.sealTemplatePlugins(c.targetTenant, targetID, setup)
	if err != nil {
		return "", err
	}
	var name pgtype.Text
	if original.Name != nil {
		name = pgtype.Text{String: *original.Name, Valid: true}
	}
	_, err = c.q.CreateEnvironmentTemplate(ctx, sqlc.CreateEnvironmentTemplateParams{ID: target, TenantID: c.target, Name: name, NetworkAccess: original.NetworkAccess, NetworkAllowedDomains: original.AllowedDomains, Files: metadata, FileContents: encrypted, Packages: packages, EnvContents: env, SetupContents: commands, Skills: skills, SkillContents: skillContents, Plugins: plugins, PluginContents: pluginContents, CapabilityDirectories: append([]string{}, setup.CapabilityDirectories...)})
	if err != nil {
		return "", err
	}
	return c.add("environment_template", id, targetID, ""), nil
}
