package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrProjectAPIKeyExists = errors.New("project API key ID already exists")

type ProjectAPIKey struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"project_id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}
type IssuedProjectAPIKey struct {
	ProjectAPIKey
	Key string `json:"key"`
}
type ProjectAPIKeyBinding struct {
	Key       ProjectAPIKey
	Principal identity.Principal
}
type ProjectAPIKeyPage struct {
	Data    []ProjectAPIKey `json:"data"`
	HasMore bool            `json:"has_more"`
}

func validProjectKeyDigest(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == digest
}
func projectKeyMetadata(row sqlc.ProjectApiKey) ProjectAPIKey {
	key := ProjectAPIKey{ID: uuid.UUID(row.ID.Bytes).String(), ProjectID: uuid.UUID(row.ProjectID.Bytes).String(), Name: row.Name, Prefix: row.Prefix, CreatedAt: row.CreatedAt.Time}
	if row.RevokedAt.Valid {
		v := row.RevokedAt.Time
		key.RevokedAt = &v
	}
	return key
}
func newProjectSecret() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := "pc_" + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(digest[:]), nil
}
func (s *Store) CreateProjectAPIKey(ctx context.Context, project, id, name string) (IssuedProjectAPIKey, error) {
	projectID, err := parseID(project)
	if err != nil {
		return IssuedProjectAPIKey{}, ErrNotFound
	}
	keyID, err := parseID(id)
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	name, err = adminResourceName(name, 80)
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	token, digest, err := newProjectSecret()
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	var result IssuedProjectAPIKey
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		p, err := q.LockProject(ctx, projectID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if p.ArchivedAt.Valid {
			return ErrProjectArchived
		}
		row, err := q.CreateProjectAPIKey(ctx, sqlc.CreateProjectAPIKeyParams{ID: keyID, Name: name, Prefix: token[:11], TokenSha256: digest, ProjectID: projectID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProjectAPIKeyExists
		}
		if err != nil {
			return err
		}
		result = IssuedProjectAPIKey{ProjectAPIKey: projectKeyMetadata(row), Key: token}
		return recordAdminMutation(ctx, q, uuid.UUID(p.TenantID.Bytes).String(), "create", "api_key", id)
	})
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	return result, nil
}
func (s *Store) ListProjectAPIKeys(ctx context.Context, project, after string, limit int, ascending bool) (ProjectAPIKeyPage, error) {
	result := ProjectAPIKeyPage{Data: []ProjectAPIKey{}}
	if limit < 1 || limit > 100 {
		return result, ErrInvalidInput
	}
	p, err := s.GetProject(ctx, project)
	if err != nil {
		return result, err
	}
	projectID, _ := parseID(p.Project.ID)
	if after != "" {
		id, err := parseID(after)
		if err != nil {
			return result, ErrNotFound
		}
		if _, err := s.queries.GetProjectAPIKeyForProject(ctx, sqlc.GetProjectAPIKeyForProjectParams{ID: id, ProjectID: projectID}); errors.Is(err, pgx.ErrNoRows) {
			return result, ErrNotFound
		} else if err != nil {
			return result, err
		}
	}
	rows, err := s.queries.ListProjectAPIKeys(ctx, sqlc.ListProjectAPIKeysParams{ProjectID: projectID, AfterID: after, Ascending: ascending, PageLimit: int32(limit + 1)})
	if err != nil {
		return result, err
	}
	result.HasMore = len(rows) > limit
	if result.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		result.Data = append(result.Data, projectKeyMetadata(row))
	}
	return result, nil
}
func (s *Store) RevokeProjectAPIKey(ctx context.Context, project, id string) error {
	projectID, err := parseID(project)
	if err != nil {
		return ErrNotFound
	}
	keyID, err := parseID(id)
	if err != nil {
		return ErrNotFound
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		p, err := q.LockProject(ctx, projectID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = q.RevokeProjectAPIKey(ctx, sqlc.RevokeProjectAPIKeyParams{ID: keyID, ProjectID: projectID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return recordAdminMutation(ctx, q, uuid.UUID(p.TenantID.Bytes).String(), "revoke", "api_key", id)
	})
}
func (s *Store) ResolveProjectAPIKey(ctx context.Context, digest string) (ProjectAPIKeyBinding, error) {
	if !validProjectKeyDigest(digest) {
		return ProjectAPIKeyBinding{}, ErrNotFound
	}
	row, err := s.queries.ResolveProjectAPIKey(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectAPIKeyBinding{}, ErrNotFound
	}
	if err != nil {
		return ProjectAPIKeyBinding{}, err
	}
	key := projectKeyMetadata(sqlc.ProjectApiKey{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, Prefix: row.Prefix, TokenSha256: row.TokenSha256, CreatedAt: row.CreatedAt, RevokedAt: row.RevokedAt})
	return ProjectAPIKeyBinding{Key: key, Principal: identity.Principal{ProjectScope: identity.ProjectScope{TenantID: uuid.UUID(row.TenantID.Bytes).String(), OrganizationID: row.OrganizationID, ProjectID: row.ExternalProjectID}, SubjectKind: row.SubjectKind, SubjectID: row.SubjectID}}, nil
}

// ValidateProjectKeySeparation checks configured credentials against all stored
// digests, including revoked credentials, before the server starts.
func (s *Store) ValidateProjectKeySeparation(ctx context.Context, digests []string) error {
	for _, digest := range digests {
		exists, err := s.queries.ProjectAPIKeyDigestExists(ctx, digest)
		if err != nil {
			return err
		}
		if exists {
			return errors.New("the Core key overlaps a persisted project API key")
		}
	}
	return nil
}
