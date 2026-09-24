package store

import (
	"context"
	"encoding/hex"
	"io"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (c *assetCopier) skill(ctx context.Context, id string) (string, error) {
	_, source, err := skillIDs(c.sourceTenant, id)
	if err != nil {
		return "", err
	}
	original, err := c.q.LockSkill(ctx, sqlc.LockSkillParams{TenantID: c.source, ID: source})
	if err != nil {
		return "", err
	}
	target := copyUUID()
	_, err = c.q.CreateAdminCopySkill(ctx, sqlc.CreateAdminCopySkillParams{ID: target, TenantID: c.target, Name: original.Name, Description: original.Description, DefaultVersion: original.DefaultVersion, LatestVersion: original.LatestVersion, NextVersion: original.NextVersion})
	if err != nil {
		return "", err
	}
	targetID := c.add("skill", id, "skill_"+copyID(target), "")
	versions, err := c.q.AdminCopySkillVersionNumbers(ctx, sqlc.AdminCopySkillVersionNumbersParams{TenantID: c.source, SkillID: source})
	if err != nil {
		return "", err
	}
	for _, number := range versions {
		row, err := c.q.ReadSkillVersion(ctx, sqlc.ReadSkillVersionParams{TenantID: c.source, SkillID: source, Version: number})
		if err != nil {
			return "", err
		}
		originalVersion, archive, err := c.s.openSkillVersion(row)
		if err != nil {
			return "", err
		}
		version, err := c.s.saveSkillVersion(ctx, c.q, c.target, target, number, agentskill.Metadata{Type: "inline", Name: row.Name, Description: row.Description}, archive)
		if err != nil {
			return "", err
		}
		c.add("skill_version", originalVersion.ID, version.ID, targetID)
	}
	return targetID, nil
}

func (c *assetCopier) file(ctx context.Context, id string) (string, error) {
	_, source, err := sourceFileIDs(c.sourceTenant, id)
	if err != nil {
		return "", err
	}
	row, err := c.q.LockInitialSourceFile(ctx, sqlc.LockInitialSourceFileParams{TenantID: c.source, ID: source})
	if err != nil {
		return "", err
	}
	if row.SizeBytes > MaxSourceFileBytes {
		return "", ErrSourceFileTooLarge
	}
	objects := c.tx.LargeObjects()
	oid, err := objects.Create(ctx, 0)
	if err != nil {
		return "", err
	}
	body, err := objects.Open(ctx, oid, pgx.LargeObjectModeWrite)
	if err != nil {
		return "", err
	}
	writer := newSourceFileWriter(body)
	err = consumeSourceFile(ctx, c.tx, row, func(_ SourceFile, reader io.Reader) error {
		_, err := io.CopyBuffer(writer, io.LimitReader(reader, MaxSourceFileBytes+1), make([]byte, sourceFileChunkBytes))
		return err
	})
	if err != nil {
		return "", err
	}
	if err = body.Close(); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(writer.hash.Sum(nil))
	if writer.size != row.SizeBytes || digest != row.Sha256 {
		return "", ErrInvalidInput
	}
	target := copyUUID()
	_, err = c.q.CreateSourceFile(ctx, sqlc.CreateSourceFileParams{ID: target, TenantID: c.target, Filename: row.Filename, Purpose: row.Purpose, BodyOid: pgtype.Uint32{Uint32: oid, Valid: true}, SizeBytes: writer.size, Sha256: digest})
	if err != nil {
		return "", err
	}
	return c.add("file", id, "file-"+copyID(target), ""), nil
}
