package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type CopyAssetsInput struct {
	ResourceType        string `json:"resource_type"`
	ResourceID          string `json:"resource_id"`
	TargetVaultID       string `json:"target_vault_id,omitempty"`
	IdempotencyKey      string `json:"-"`
	IncludeDependencies bool   `json:"include_dependencies"`
}

type AssetCopyMapping struct {
	Type     string `json:"type"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
}

type AssetCopySkipped struct {
	Type     string `json:"type"`
	SourceID string `json:"source_id"`
	Reason   string `json:"reason"`
}

type CopyAssetsResult struct {
	Mappings []AssetCopyMapping `json:"mappings"`
	Skipped  []AssetCopySkipped `json:"skipped"`
}

// CopyAssets is the management-only atomic boundary. Callers resolve source and
// target authority before entering; every resource lookup remains tenant scoped.
func (s *Store) CopyAssets(ctx context.Context, sourceTenant, targetTenant string, input CopyAssetsInput) (CopyAssetsResult, error) {
	source, err := parseID(sourceTenant)
	if err != nil {
		return CopyAssetsResult{}, err
	}
	target, err := parseID(targetTenant)
	if err != nil {
		return CopyAssetsResult{}, err
	}
	if source == target || input.ResourceID == "" || len(input.IdempotencyKey) > 256 || !utf8.ValidString(input.IdempotencyKey) || strings.ContainsRune(input.IdempotencyKey, 0) {
		return CopyAssetsResult{}, ErrInvalidInput
	}
	switch input.ResourceType {
	case "agent", "skill", "environment_template", "file", "vault":
		if input.TargetVaultID != "" {
			return CopyAssetsResult{}, ErrInvalidInput
		}
	case "credential":
		if input.TargetVaultID == "" {
			return CopyAssetsResult{}, ErrInvalidInput
		}
	default:
		return CopyAssetsResult{}, ErrInvalidInput
	}
	sourceTenant, targetTenant = uuid.UUID(source.Bytes).String(), uuid.UUID(target.Bytes).String()
	request, err := json.Marshal(struct {
		Source string
		Input  CopyAssetsInput
	}{sourceTenant, input})
	if err != nil {
		return CopyAssetsResult{}, err
	}
	digest := sha256.Sum256(request)
	var result CopyAssetsResult
	// A consistent snapshot covers the root and every dependency. Serialization
	// retries restart the complete transaction, including all large objects.
	for attempt := 0; attempt < 3; attempt++ {
		result = CopyAssetsResult{Mappings: []AssetCopyMapping{}, Skipped: []AssetCopySkipped{}}
		err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			q := s.queries.WithTx(tx)
			project, err := q.LockAdminCopyTargetProject(ctx, target)
			if err != nil {
				return err
			}
			if project.ArchivedAt.Valid {
				return ErrProjectArchived
			}
			if input.IdempotencyKey != "" {
				if err := q.LockAdminAssetCopy(ctx, "admin-copy:"+targetTenant+":"+input.IdempotencyKey); err != nil {
					return err
				}
				existing, err := q.GetAdminAssetCopy(ctx, sqlc.GetAdminAssetCopyParams{TargetTenantID: target, IdempotencyKey: input.IdempotencyKey})
				if err == nil {
					if !bytes.Equal(existing.RequestHash, digest[:]) {
						return ErrIdempotencyConflict
					}
					return json.Unmarshal(existing.Result, &result)
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
			copier := assetCopier{s: s, tx: tx, q: q, source: source, target: target, sourceTenant: sourceTenant, targetTenant: targetTenant, dependencies: input.IncludeDependencies, result: result, mapped: map[string]string{}, parents: map[string]string{}}
			if _, err := copier.copy(ctx, input.ResourceType, input.ResourceID, input.TargetVaultID); err != nil {
				return err
			}
			result = copier.result
			mappings, err := json.Marshal(result.Mappings)
			if err != nil {
				return err
			}
			audit, err := recordAdminMutation(ctx, q, targetTenant, "copy", input.ResourceType, input.ResourceID, mappings)
			if err != nil {
				return err
			}
			auditID, err := parseID(audit)
			if err != nil {
				return err
			}
			for _, mapping := range result.Mappings {
				if err := q.CreateAdminResourceOwner(ctx, sqlc.CreateAdminResourceOwnerParams{TenantID: target, ResourceType: mapping.Type, ResourceID: mapping.TargetID, ParentID: copier.parents[mapping.Type+":"+mapping.TargetID], AuditID: auditID}); err != nil {
					return err
				}
			}
			if input.IdempotencyKey != "" {
				raw, err := json.Marshal(result)
				if err != nil {
					return err
				}
				return q.SaveAdminAssetCopy(ctx, sqlc.SaveAdminAssetCopyParams{TargetTenantID: target, IdempotencyKey: input.IdempotencyKey, RequestHash: digest[:], Result: raw, AuditID: auditID})
			}
			return nil
		})
		var postgres *pgconn.PgError
		if !errors.As(err, &postgres) || postgres.Code != "40001" {
			break
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return CopyAssetsResult{}, err
	}
	return result, nil
}

type assetCopier struct {
	s                          *Store
	tx                         pgx.Tx
	q                          *sqlc.Queries
	source, target             pgtype.UUID
	sourceTenant, targetTenant string
	dependencies               bool
	result                     CopyAssetsResult
	mapped                     map[string]string
	parents                    map[string]string
}

func (c *assetCopier) add(kind, source, target, parent string) string {
	c.mapped[kind+":"+source] = target
	c.parents[kind+":"+target] = parent
	c.result.Mappings = append(c.result.Mappings, AssetCopyMapping{Type: kind, SourceID: source, TargetID: target})
	return target
}

func (c *assetCopier) copy(ctx context.Context, kind, id, vault string) (string, error) {
	switch kind {
	case "agent", "vault", "credential", "environment_template":
		source, err := parseID(id)
		if err != nil {
			return "", ErrNotFound
		}
		id = copyID(source)
	}
	if target, ok := c.mapped[kind+":"+id]; ok {
		return target, nil
	}
	switch kind {
	case "skill":
		return c.skill(ctx, id)
	case "file":
		return c.file(ctx, id)
	case "vault":
		return c.vault(ctx, id)
	case "credential":
		return c.credential(ctx, id, vault)
	case "environment_template":
		return c.template(ctx, id)
	case "agent":
		return c.agent(ctx, id)
	default:
		return "", ErrInvalidInput
	}
}

func copyUUID() pgtype.UUID        { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }
func copyID(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }
