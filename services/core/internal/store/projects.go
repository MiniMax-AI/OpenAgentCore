package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrProjectArchived = errors.New("project is archived")
	ErrProjectExists   = errors.New("project ID already exists")
)

type Project struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	CreatedAt      time.Time  `json:"created_at"`
	ArchivedAt     *time.Time `json:"archived_at"`
	ActiveKeyCount int64      `json:"active_key_count"`
	TenantID       string     `json:"-"`
}
type ProjectBinding struct {
	Project   Project
	Principal identity.Principal
}
type ProjectPage struct {
	Data    []Project `json:"data"`
	HasMore bool      `json:"has_more"`
}

func projectBinding(row sqlc.GetProjectRow) ProjectBinding {
	p := Project{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name, TenantID: uuid.UUID(row.TenantID.Bytes).String(), CreatedAt: row.CreatedAt.Time, ActiveKeyCount: row.ActiveKeyCount}
	if row.ArchivedAt.Valid {
		t := row.ArchivedAt.Time
		p.ArchivedAt = &t
	}
	return ProjectBinding{Project: p, Principal: identity.Principal{ProjectScope: identity.ProjectScope{TenantID: p.TenantID, OrganizationID: row.OrganizationID, ProjectID: row.ExternalProjectID}, SubjectKind: row.SubjectKind, SubjectID: row.SubjectID}}
}
func adminResourceName(value string, max int) (string, error) {
	control := strings.ContainsFunc(value, unicode.IsControl)
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > max || control {
		return "", &AdminValidationError{Code: "invalid_name", Param: "name", MaxLength: max, message: fmt.Sprintf("%s: name must contain 1–%d characters without controls", ErrInvalidInput, max)}
	}
	return value, nil
}
func (s *Store) CreateProject(ctx context.Context, id, name string) (Project, error) {
	projectID, err := parseID(id)
	if err != nil {
		return Project{}, err
	}
	name, err = adminResourceName(name, 128)
	if err != nil {
		return Project{}, err
	}
	tenantID, _ := parseID(uuid.NewString())
	var result Project
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		_, err := q.EnsureProjectScope(ctx, sqlc.EnsureProjectScopeParams{TenantID: tenantID, OrganizationID: "core", ProjectID: "proj_" + id})
		if err != nil {
			var db *pgconn.PgError
			if errors.As(err, &db) && db.Code == "23505" {
				return ErrProjectExists
			}
			return err
		}
		_, err = q.CreateProject(ctx, sqlc.CreateProjectParams{ID: projectID, Name: name, TenantID: tenantID, SubjectID: "project:" + id})
		if err != nil {
			return err
		}
		row, err := q.GetProject(ctx, projectID)
		if err != nil {
			return err
		}
		result = projectBinding(row).Project
		return recordAdminMutation(ctx, q, result.TenantID, "create", "project", id)
	})
	if err != nil {
		return Project{}, err
	}
	return result, nil
}
func (s *Store) GetProject(ctx context.Context, id string) (ProjectBinding, error) {
	projectID, err := parseID(id)
	if err != nil {
		return ProjectBinding{}, ErrNotFound
	}
	row, err := s.queries.GetProject(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return ProjectBinding{}, err
	}
	return projectBinding(row), nil
}
func (s *Store) ListProjects(ctx context.Context, after string, limit int, ascending bool) (ProjectPage, error) {
	result := ProjectPage{Data: []Project{}}
	if limit < 1 || limit > 100 {
		return result, ErrInvalidInput
	}
	if after != "" {
		if _, err := s.GetProject(ctx, after); err != nil {
			return result, err
		}
	}
	rows, err := s.queries.ListProjects(ctx, sqlc.ListProjectsParams{AfterID: after, Ascending: ascending, PageLimit: int32(limit + 1)})
	if err != nil {
		return result, err
	}
	result.HasMore = len(rows) > limit
	if result.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		result.Data = append(result.Data, projectBinding(sqlc.GetProjectRow(row)).Project)
	}
	return result, nil
}
func (s *Store) RenameProject(ctx context.Context, id, name string) (Project, error) {
	name, err := adminResourceName(name, 128)
	if err != nil {
		return Project{}, err
	}
	return s.mutateProject(ctx, id, name, false)
}
func (s *Store) ArchiveProject(ctx context.Context, id string) (Project, error) {
	return s.mutateProject(ctx, id, "", true)
}
func (s *Store) mutateProject(ctx context.Context, id, name string, archive bool) (Project, error) {
	projectID, err := parseID(id)
	if err != nil {
		return Project{}, ErrNotFound
	}
	var result Project
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		_, err := q.LockProjectForUpdate(ctx, projectID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		action := "rename"
		if archive {
			action = "archive"
			if err := q.ArchiveProject(ctx, projectID); err != nil {
				return err
			}
			if err := q.RevokeProjectKeys(ctx, projectID); err != nil {
				return err
			}
		} else {
			if err := q.RenameProject(ctx, sqlc.RenameProjectParams{ID: projectID, Name: name}); err != nil {
				return err
			}
		}
		row, err := q.GetProject(ctx, projectID)
		if err != nil {
			return err
		}
		result = projectBinding(row).Project
		return recordAdminMutation(ctx, q, result.TenantID, action, "project", id)
	})
	if err != nil {
		return Project{}, err
	}
	return result, nil
}
