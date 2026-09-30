package environmenttemplates

import (
	"context"
	"errors"
)

// Service runs the Template write operations.
type Service struct {
	storage Storage
}

func NewService(storage Storage) (*Service, error) {
	if storage == nil {
		return nil, errors.New("environmenttemplates: storage is required")
	}
	return &Service{storage: storage}, nil
}

type CreateCommand struct {
	TenantID string
	Input    Input
}

type UpdateCommand struct {
	TenantID   string
	TemplateID string
	Input      Input
}

type DeleteCommand struct {
	TenantID   string
	TemplateID string
}

// Create saves a new Template. Input without a network policy gets the default.
func (s *Service) Create(ctx context.Context, cmd CreateCommand) (Template, error) {
	in := cmd.Input.withDefaultNetwork()
	if err := in.Validate(); err != nil {
		return Template{}, err
	}
	return s.storage.Create(ctx, cmd.TenantID, in)
}

// Update replaces the fields the input sets. Invalid input is rejected before
// the Template is looked up.
func (s *Service) Update(ctx context.Context, cmd UpdateCommand) (Template, error) {
	if err := cmd.Input.Validate(); err != nil {
		return Template{}, err
	}
	return s.storage.Update(ctx, cmd.TenantID, cmd.TemplateID, cmd.Input)
}

// Delete deletes a Template and returns its ID. Sessions created from it keep
// their frozen configuration.
func (s *Service) Delete(ctx context.Context, cmd DeleteCommand) (string, error) {
	return s.storage.Delete(ctx, cmd.TenantID, cmd.TemplateID)
}
