// Package projectpg stores Projects and their API keys in PostgreSQL. It
// implements projects.Storage and projects.Reader.
package projectpg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
)

type Store struct {
	pool *pgunit.Pool
}

var (
	_ projects.Storage = (*Store)(nil)
	_ projects.Reader  = (*Store)(nil)
)

func New(pool *pgunit.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateProject(ctx context.Context, project projects.NewProject) (projects.Project, error) {
	id, err := pgunit.ParseID(project.ID)
	if err != nil {
		return projects.Project{}, projects.ErrInvalidInput
	}
	tenant := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	var result projects.Project
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		// The scope insert conflicts on the external Project ID, which the
		// Project ID determines.
		if _, err := q.EnsureProjectScope(ctx, sqlc.EnsureProjectScopeParams{TenantID: tenant, OrganizationID: project.OrganizationID, ProjectID: project.ExternalProjectID}); err != nil {
			var db *pgconn.PgError
			if errors.As(err, &db) && db.Code == "23505" {
				return projects.ErrExists
			}
			return err
		}
		if _, err := q.CreateProject(ctx, sqlc.CreateProjectParams{ID: id, Name: project.Name, TenantID: tenant, SubjectID: project.SubjectID}); err != nil {
			return err
		}
		row, err := q.GetProject(ctx, id)
		if err != nil {
			return err
		}
		result = binding(row).Project
		return auditpg.RecordAdminMutation(ctx, q, result.TenantID, "create", "project", project.ID)
	})
	if err != nil {
		return projects.Project{}, err
	}
	return result, nil
}

func (s *Store) RenameProject(ctx context.Context, id, name string) (projects.Project, error) {
	return s.updateProject(ctx, id, "rename", func(ctx context.Context, q *sqlc.Queries, project pgtype.UUID) error {
		return q.RenameProject(ctx, sqlc.RenameProjectParams{ID: project, Name: name})
	})
}

func (s *Store) ArchiveProject(ctx context.Context, id string) (projects.Project, error) {
	return s.updateProject(ctx, id, "archive", func(ctx context.Context, q *sqlc.Queries, project pgtype.UUID) error {
		if err := q.ArchiveProject(ctx, project); err != nil {
			return err
		}
		return q.RevokeProjectKeys(ctx, project)
	})
}

// updateProject applies one audited change under the Project's row lock.
func (s *Store) updateProject(ctx context.Context, id, action string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) (projects.Project, error) {
	project := pgunit.PathID(id)
	var result projects.Project
	err := s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.LockProjectForUpdate(ctx, project); errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
		} else if err != nil {
			return err
		}
		if err := apply(ctx, q, project); err != nil {
			return err
		}
		row, err := q.GetProject(ctx, project)
		if err != nil {
			return err
		}
		result = binding(row).Project
		return auditpg.RecordAdminMutation(ctx, q, result.TenantID, action, "project", id)
	})
	if err != nil {
		return projects.Project{}, err
	}
	return result, nil
}

func (s *Store) WithKeyIssuance(ctx context.Context, projectID string, issue func(projects.KeyIssuanceTx) error) error {
	project := pgunit.PathID(projectID)
	return s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.LockProject(ctx, project)
		if errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
		}
		if err != nil {
			return err
		}
		return issue(&keyIssuance{q: q, project: row})
	})
}

// keyIssuance holds the Project row that WithKeyIssuance locked.
type keyIssuance struct {
	q       *sqlc.Queries
	project sqlc.Project
}

func (k *keyIssuance) LoadProject(context.Context) (projects.LockedProject, error) {
	return projects.LockedProject{Archived: k.project.ArchivedAt.Valid}, nil
}

func (k *keyIssuance) ApplyAPIKey(ctx context.Context, key projects.NewAPIKey) (projects.APIKey, error) {
	id, err := pgunit.ParseID(key.ID)
	if err != nil {
		return projects.APIKey{}, projects.ErrInvalidInput
	}
	row, err := k.q.CreateProjectAPIKey(ctx, sqlc.CreateProjectAPIKeyParams{ID: id, Name: key.Name, Prefix: key.Prefix, TokenSha256: hex.EncodeToString(key.Digest[:]), ProjectID: k.project.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.APIKey{}, projects.ErrAPIKeyExists
	}
	if err != nil {
		return projects.APIKey{}, err
	}
	if err := auditpg.RecordAdminMutation(ctx, k.q, uuid.UUID(k.project.TenantID.Bytes).String(), "create", "api_key", key.ID); err != nil {
		return projects.APIKey{}, err
	}
	return apiKey(row), nil
}

func (s *Store) RevokeAPIKey(ctx context.Context, projectID, keyID string) error {
	project := pgunit.PathID(projectID)
	return s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.LockProject(ctx, project)
		if errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := q.RevokeProjectAPIKey(ctx, sqlc.RevokeProjectAPIKeyParams{ID: pgunit.PathID(keyID), ProjectID: project}); errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
		} else if err != nil {
			return err
		}
		return auditpg.RecordAdminMutation(ctx, q, uuid.UUID(row.TenantID.Bytes).String(), "revoke", "api_key", keyID)
	})
}

func (s *Store) GetProject(ctx context.Context, id string) (projects.Binding, error) {
	row, err := s.pool.Queries().GetProject(ctx, pgunit.PathID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.Binding{}, projects.ErrNotFound
	}
	if err != nil {
		return projects.Binding{}, err
	}
	return binding(row), nil
}

func (s *Store) ListProjects(ctx context.Context, query projects.ListQuery) (projects.Page, error) {
	result := projects.Page{Data: []projects.Project{}}
	if err := query.Validate(); err != nil {
		return result, err
	}
	var rows []sqlc.ListProjectsRow
	err := s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if query.After != "" {
			if _, err := q.GetProject(ctx, pgunit.PathID(query.After)); errors.Is(err, pgx.ErrNoRows) {
				return projects.ErrNotFound
			} else if err != nil {
				return err
			}
		}
		var err error
		rows, err = q.ListProjects(ctx, sqlc.ListProjectsParams{AfterID: query.After, Ascending: query.Ascending, PageLimit: int32(query.Limit + 1)})
		return err
	})
	if err != nil {
		return result, err
	}
	result.HasMore = len(rows) > query.Limit
	if result.HasMore {
		rows = rows[:query.Limit]
	}
	for _, row := range rows {
		result.Data = append(result.Data, binding(sqlc.GetProjectRow(row)).Project)
	}
	return result, nil
}

func (s *Store) ListAPIKeys(ctx context.Context, projectID string, query projects.ListQuery) (projects.KeyPage, error) {
	result := projects.KeyPage{Data: []projects.APIKey{}}
	if err := query.Validate(); err != nil {
		return result, err
	}
	project := pgunit.PathID(projectID)
	var rows []sqlc.ProjectApiKey
	err := s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.GetProject(ctx, project); errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
		} else if err != nil {
			return err
		}
		if query.After != "" {
			if _, err := q.GetProjectAPIKeyForProject(ctx, sqlc.GetProjectAPIKeyForProjectParams{ID: pgunit.PathID(query.After), ProjectID: project}); errors.Is(err, pgx.ErrNoRows) {
				return projects.ErrNotFound
			} else if err != nil {
				return err
			}
		}
		var err error
		rows, err = q.ListProjectAPIKeys(ctx, sqlc.ListProjectAPIKeysParams{ProjectID: project, AfterID: query.After, Ascending: query.Ascending, PageLimit: int32(query.Limit + 1)})
		return err
	})
	if err != nil {
		return result, err
	}
	result.HasMore = len(rows) > query.Limit
	if result.HasMore {
		rows = rows[:query.Limit]
	}
	for _, row := range rows {
		result.Data = append(result.Data, apiKey(row))
	}
	return result, nil
}

func (s *Store) ResolveAPIKey(ctx context.Context, digest [sha256.Size]byte) (projects.KeyBinding, error) {
	row, err := s.pool.Queries().ResolveProjectAPIKey(ctx, hex.EncodeToString(digest[:]))
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.KeyBinding{}, projects.ErrNotFound
	}
	if err != nil {
		return projects.KeyBinding{}, err
	}
	key := apiKey(sqlc.ProjectApiKey{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, Prefix: row.Prefix, CreatedAt: row.CreatedAt, RevokedAt: row.RevokedAt})
	return projects.KeyBinding{Key: key, Principal: identity.Principal{ProjectScope: identity.ProjectScope{TenantID: uuid.UUID(row.TenantID.Bytes).String(), OrganizationID: row.OrganizationID, ProjectID: row.ExternalProjectID}, SubjectKind: row.SubjectKind, SubjectID: row.SubjectID}}, nil
}

func (s *Store) APIKeyDigestExists(ctx context.Context, digest [sha256.Size]byte) (bool, error) {
	return s.pool.Queries().ProjectAPIKeyDigestExists(ctx, hex.EncodeToString(digest[:]))
}

func binding(row sqlc.GetProjectRow) projects.Binding {
	p := projects.Project{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name, TenantID: uuid.UUID(row.TenantID.Bytes).String(), CreatedAt: row.CreatedAt.Time, ActiveKeyCount: row.ActiveKeyCount}
	if row.ArchivedAt.Valid {
		t := row.ArchivedAt.Time
		p.ArchivedAt = &t
	}
	return projects.Binding{Project: p, Principal: identity.Principal{ProjectScope: identity.ProjectScope{TenantID: p.TenantID, OrganizationID: row.OrganizationID, ProjectID: row.ExternalProjectID}, SubjectKind: row.SubjectKind, SubjectID: row.SubjectID}}
}

func apiKey(row sqlc.ProjectApiKey) projects.APIKey {
	key := projects.APIKey{ID: uuid.UUID(row.ID.Bytes).String(), ProjectID: uuid.UUID(row.ProjectID.Bytes).String(), Name: row.Name, Prefix: row.Prefix, CreatedAt: row.CreatedAt.Time}
	if row.RevokedAt.Valid {
		t := row.RevokedAt.Time
		key.RevokedAt = &t
	}
	return key
}
