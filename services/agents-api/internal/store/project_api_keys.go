package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrProjectAPIKeyExists = errors.New("project API key ID already exists")

// ProjectAPIKey contains only the metadata safe to return after issuance.
type ProjectAPIKey struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

type IssuedProjectAPIKey struct {
	ProjectAPIKey
	Key string `json:"key"`
}

// ProjectAPIKeyBinding must also match a currently configured static parent before authentication.
type ProjectAPIKeyBinding struct {
	BindingDigest string
	Principal     identity.Principal
}

func projectAPIKeyOwner(bindingDigest string, principal identity.Principal) (pgtype.UUID, error) {
	if !validProjectKeyDigest(bindingDigest) {
		return pgtype.UUID{}, fmt.Errorf("%w: invalid parent binding digest", ErrInvalidInput)
	}
	if err := principal.Validate(); err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return parseID(principal.TenantID)
}

func validProjectKeyDigest(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == digest
}

func projectAPIKeyMetadata(id pgtype.UUID, name, prefix string, created, revoked pgtype.Timestamptz) ProjectAPIKey {
	result := ProjectAPIKey{ID: uuid.UUID(id.Bytes).String(), Name: name, Prefix: prefix, CreatedAt: created.Time}
	if revoked.Valid {
		value := revoked.Time
		result.RevokedAt = &value
	}
	return result
}

// CreateProjectAPIKey persists only a hash; a repeated ID cannot recover or replace the secret.
func (s *Store) CreateProjectAPIKey(ctx context.Context, bindingDigest string, principal identity.Principal, id, name string) (IssuedProjectAPIKey, error) {
	tenant, err := projectAPIKeyOwner(bindingDigest, principal)
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	keyID, err := parseID(id)
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	containsControl := strings.ContainsFunc(name, unicode.IsControl)
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || containsControl {
		return IssuedProjectAPIKey{}, fmt.Errorf("%w: key name must contain 1–80 characters without controls", ErrInvalidInput)
	}
	if err := s.EnsureProjectScopes(ctx, []identity.ProjectScope{principal.ProjectScope}); err != nil {
		return IssuedProjectAPIKey{}, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return IssuedProjectAPIKey{}, err
	}
	token := "pc_" + base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	row, err := s.queries.CreateProjectAPIKey(ctx, sqlc.CreateProjectAPIKeyParams{
		ID: keyID, Name: name, Prefix: token[:11], TokenSha256: hex.EncodeToString(digest[:]),
		BindingDigest: bindingDigest, TenantID: tenant, OrganizationID: principal.OrganizationID,
		ProjectID: principal.ProjectID, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return IssuedProjectAPIKey{}, ErrProjectAPIKeyExists
	}
	if err != nil {
		return IssuedProjectAPIKey{}, err
	}
	return IssuedProjectAPIKey{ProjectAPIKey: projectAPIKeyMetadata(row.ID, row.Name, row.Prefix, row.CreatedAt, row.RevokedAt), Key: token}, nil
}

func (s *Store) ListProjectAPIKeys(ctx context.Context, bindingDigest string, principal identity.Principal) ([]ProjectAPIKey, error) {
	tenant, err := projectAPIKeyOwner(bindingDigest, principal)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListProjectAPIKeys(ctx, sqlc.ListProjectAPIKeysParams{
		BindingDigest: bindingDigest, TenantID: tenant, OrganizationID: principal.OrganizationID,
		ProjectID: principal.ProjectID, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID,
	})
	if err != nil {
		return nil, err
	}
	result := make([]ProjectAPIKey, 0, len(rows))
	for _, row := range rows {
		result = append(result, projectAPIKeyMetadata(row.ID, row.Name, row.Prefix, row.CreatedAt, row.RevokedAt))
	}
	return result, nil
}

func (s *Store) RevokeProjectAPIKey(ctx context.Context, bindingDigest string, principal identity.Principal, id string) error {
	tenant, err := projectAPIKeyOwner(bindingDigest, principal)
	if err != nil {
		return err
	}
	keyID, err := parseID(id)
	if err != nil {
		return err
	}
	count, err := s.queries.RevokeProjectAPIKey(ctx, sqlc.RevokeProjectAPIKeyParams{
		ID: keyID, BindingDigest: bindingDigest, TenantID: tenant, OrganizationID: principal.OrganizationID,
		ProjectID: principal.ProjectID, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID,
	})
	if err == nil && count == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ResolveProjectAPIKey(ctx context.Context, tokenDigest string) (ProjectAPIKeyBinding, error) {
	if !validProjectKeyDigest(tokenDigest) {
		return ProjectAPIKeyBinding{}, ErrNotFound
	}
	row, err := s.queries.ResolveProjectAPIKey(ctx, tokenDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectAPIKeyBinding{}, ErrNotFound
	}
	if err != nil {
		return ProjectAPIKeyBinding{}, err
	}
	return ProjectAPIKeyBinding{BindingDigest: row.BindingDigest, Principal: identity.Principal{
		ProjectScope: identity.ProjectScope{TenantID: uuid.UUID(row.TenantID.Bytes).String(), OrganizationID: row.OrganizationID, ProjectID: row.ProjectID},
		SubjectKind:  row.SubjectKind, SubjectID: row.SubjectID,
	}}, nil
}
