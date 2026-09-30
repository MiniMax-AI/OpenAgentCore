package projects

import (
	"context"
	"errors"
)

// Service runs the Project and key administration use cases. Reads go to the
// Reader directly.
type Service struct {
	storage Storage
}

func NewService(storage Storage) (*Service, error) {
	if storage == nil {
		return nil, errors.New("projects: storage is required")
	}
	return &Service{storage: storage}, nil
}

// CreateProject creates an empty Project. The caller chooses its ID.
type CreateProject struct {
	ID, Name string
}

func (s *Service) CreateProject(ctx context.Context, command CreateProject) (Project, error) {
	name, err := normalizeName(command.Name, ProjectNameMaxLength)
	if err != nil {
		return Project{}, err
	}
	return s.storage.CreateProject(ctx, newProject(command.ID, name))
}

// RenameProject changes a Project's display name, archived or not.
type RenameProject struct {
	ID, Name string
}

func (s *Service) RenameProject(ctx context.Context, command RenameProject) (Project, error) {
	name, err := normalizeName(command.Name, ProjectNameMaxLength)
	if err != nil {
		return Project{}, err
	}
	return s.storage.RenameProject(ctx, command.ID, name)
}

// ArchiveProject archives a Project and revokes all of its keys. Its assets
// remain.
type ArchiveProject struct {
	ID string
}

func (s *Service) ArchiveProject(ctx context.Context, command ArchiveProject) (Project, error) {
	return s.storage.ArchiveProject(ctx, command.ID)
}

// CreateAPIKey issues a new secret in an active Project. The caller chooses
// the key ID.
type CreateAPIKey struct {
	ProjectID, ID, Name string
}

func (s *Service) CreateAPIKey(ctx context.Context, command CreateAPIKey) (IssuedAPIKey, error) {
	name, err := normalizeName(command.Name, KeyNameMaxLength)
	if err != nil {
		return IssuedAPIKey{}, err
	}
	secret, digest, err := newSecret()
	if err != nil {
		return IssuedAPIKey{}, err
	}
	var issued IssuedAPIKey
	err = s.storage.WithKeyIssuance(ctx, command.ProjectID, func(tx KeyIssuanceTx) error {
		project, err := tx.LoadProject(ctx)
		if err != nil {
			return err
		}
		if project.Archived {
			return ErrArchived
		}
		key, err := tx.ApplyAPIKey(ctx, NewAPIKey{ID: command.ID, Name: name, Prefix: secret[:keyPrefixLength], Digest: digest})
		if err != nil {
			return err
		}
		issued = IssuedAPIKey{APIKey: key, Key: secret}
		return nil
	})
	if err != nil {
		return IssuedAPIKey{}, err
	}
	return issued, nil
}

// RevokeAPIKey revokes one key. Revoking a revoked key succeeds.
type RevokeAPIKey struct {
	ProjectID, ID string
}

func (s *Service) RevokeAPIKey(ctx context.Context, command RevokeAPIKey) error {
	return s.storage.RevokeAPIKey(ctx, command.ProjectID, command.ID)
}
