package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrProjectAPIKeyExists = errors.New("project API key ID or reset request already exists")

type ProjectAPIKey struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Prefix         string     `json:"prefix"`
	Kind           string     `json:"kind"`
	TenantID       string     `json:"tenant_id"`
	OrganizationID string     `json:"organization_id"`
	ProjectID      string     `json:"project_id"`
	CreatedAt      time.Time  `json:"created_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
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
func projectAPIKeyBinding(row sqlc.ProjectApiKey) ProjectAPIKeyBinding {
	key := ProjectAPIKey{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name, Prefix: row.Prefix, Kind: "issued", TenantID: uuid.UUID(row.TenantID.Bytes).String(), OrganizationID: row.OrganizationID, ProjectID: row.ProjectID, CreatedAt: row.CreatedAt.Time}
	if row.RevokedAt.Valid {
		t := row.RevokedAt.Time
		key.RevokedAt = &t
	}
	return ProjectAPIKeyBinding{Key: key, Principal: identity.Principal{ProjectScope: identity.ProjectScope{TenantID: key.TenantID, OrganizationID: key.OrganizationID, ProjectID: key.ProjectID}, SubjectKind: "service_account", SubjectID: key.ID}}
}
func newProjectSecret() (string, string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", err
	}
	token := "pc_" + base64.RawURLEncoding.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(hash[:]), nil
}
func (s *Store) CreateProjectAPIKey(ctx context.Context, id, name string) (IssuedProjectAPIKey, error) {
	keyID, err := parseID(id)
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	control := strings.ContainsFunc(name, unicode.IsControl)
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || control {
		return IssuedProjectAPIKey{}, fmt.Errorf("%w: key name must contain 1–80 characters without controls", ErrInvalidInput)
	}
	token, digest, err := newProjectSecret()
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	tenantID, _ := parseID(uuid.NewString())
	var result IssuedProjectAPIKey
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		_, err := q.EnsureProjectScope(ctx, sqlc.EnsureProjectScopeParams{TenantID: tenantID, OrganizationID: "core", ProjectID: "key_" + id})
		if err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "23505" {
				return ErrProjectAPIKeyExists
			}
			return err
		}
		row, err := q.CreateProjectAPIKey(ctx, sqlc.CreateProjectAPIKeyParams{ID: keyID, Name: name, Prefix: token[:11], TokenSha256: digest, TenantID: tenantID, OrganizationID: "core", ProjectID: "key_" + id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProjectAPIKeyExists
		}
		if err != nil {
			return err
		}
		result = IssuedProjectAPIKey{ProjectAPIKey: projectAPIKeyBinding(row).Key, Key: token}
		_, err = recordAdminMutation(ctx, q, result.TenantID, "create", "api_key", id, nil)
		return err
	})
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	return result, nil
}
func (s *Store) GetProjectAPIKey(ctx context.Context, id string) (ProjectAPIKeyBinding, error) {
	keyID, err := parseID(id)
	if err != nil {
		return ProjectAPIKeyBinding{}, ErrNotFound
	}
	row, err := s.queries.GetProjectAPIKey(ctx, keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return ProjectAPIKeyBinding{}, err
	}
	return projectAPIKeyBinding(row), nil
}
func (s *Store) ListProjectAPIKeys(ctx context.Context, after string, limit int, ascending bool) (ProjectAPIKeyPage, error) {
	if limit < 1 || limit > 500 {
		return ProjectAPIKeyPage{}, ErrInvalidInput
	}
	rows, err := s.queries.ListProjectAPIKeys(ctx, sqlc.ListProjectAPIKeysParams{AfterID: after, Ascending: ascending, PageLimit: int32(limit + 1)})
	if err != nil {
		return ProjectAPIKeyPage{}, err
	}
	result := ProjectAPIKeyPage{Data: []ProjectAPIKey{}, HasMore: len(rows) > limit}
	if result.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		result.Data = append(result.Data, projectAPIKeyBinding(row).Key)
	}
	return result, nil
}
func (s *Store) ResetProjectAPIKey(ctx context.Context, id, requestID string) (IssuedProjectAPIKey, error) {
	keyID, err := parseID(id)
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	if !auditText(requestID, 128, true) {
		return IssuedProjectAPIKey{}, ErrInvalidInput
	}
	source, ok := adminaudit.FromContext(ctx)
	if !ok {
		return IssuedProjectAPIKey{}, ErrInvalidInput
	}
	source.RequestID = requestID
	ctx = adminaudit.WithSource(ctx, source)
	token, digest, err := newProjectSecret()
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	var result IssuedProjectAPIKey
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.ResetProjectAPIKey(ctx, sqlc.ResetProjectAPIKeyParams{ID: keyID, Prefix: token[:11], TokenSha256: digest})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		duplicate, err := q.AdminResetRequestExists(ctx, sqlc.AdminResetRequestExistsParams{TenantID: row.TenantID, RequestID: requestID, ResourceID: id})
		if err != nil {
			return err
		}
		if duplicate {
			return ErrProjectAPIKeyExists
		}
		result = IssuedProjectAPIKey{ProjectAPIKey: projectAPIKeyBinding(row).Key, Key: token}
		_, err = recordAdminMutation(ctx, q, result.TenantID, "reset", "api_key", id, nil)
		return err
	})
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	return result, nil
}
func (s *Store) RevokeProjectAPIKey(ctx context.Context, id string) error {
	keyID, err := parseID(id)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.RevokeProjectAPIKey(ctx, keyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = recordAdminMutation(ctx, q, uuid.UUID(row.TenantID.Bytes).String(), "revoke", "api_key", id, nil)
		return err
	})
}
func (s *Store) ResolveProjectAPIKey(ctx context.Context, digest string) (ProjectAPIKeyBinding, error) {
	if !validProjectKeyDigest(digest) {
		return ProjectAPIKeyBinding{}, ErrNotFound
	}
	row, err := s.queries.ResolveProjectAPIKey(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return ProjectAPIKeyBinding{}, err
	}
	return projectAPIKeyBinding(row), nil
}

// ValidateProjectKeySeparation checks all persisted keys, including revoked keys.
func (s *Store) ValidateProjectKeySeparation(ctx context.Context, digests, tenants []string) error {
	for _, digest := range digests {
		exists, err := s.queries.ProjectAPIKeyDigestExists(ctx, digest)
		if err != nil {
			return err
		}
		if exists {
			return errors.New("administrator or static credential overlaps a persisted API key")
		}
	}
	for _, tenant := range tenants {
		id, err := parseID(tenant)
		if err != nil {
			return err
		}
		exists, err := s.queries.ProjectAPIKeyTenantExists(ctx, id)
		if err != nil {
			return err
		}
		if exists {
			return errors.New("static API key overlaps an issued key space")
		}
	}
	return nil
}
