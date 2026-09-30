package agents

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// Service runs the Agent write use cases. Reads go through Reader.
type Service struct {
	storage Storage
}

func NewService(storage Storage) (*Service, error) {
	if storage == nil {
		return nil, errors.New("agents: storage is required")
	}
	return &Service{storage: storage}, nil
}

// CreateCommand saves a new Agent. Configuration is the resolved saved
// configuration; ModelProvider is the bundle its x_agents_core.model_provider
// describes. Each call creates a new Agent.
type CreateCommand struct {
	TenantID      string
	Metadata      map[string]string
	Configuration json.RawMessage
	ModelProvider *v1.ModelProviderInput
}

func (s *Service) Create(ctx context.Context, cmd CreateCommand) (Agent, error) {
	metadata, err := encodeMetadata(cmd.Metadata)
	if err != nil {
		return Agent{}, err
	}
	configuration, err := createConfiguration(cmd.Configuration)
	if err != nil {
		return Agent{}, err
	}
	if err := validateModelExecution(configuration, cmd.ModelProvider); err != nil {
		return Agent{}, err
	}
	return s.storage.CreateAgent(ctx, NewAgent{TenantID: cmd.TenantID, Metadata: metadata, Configuration: configuration, ModelProvider: cmd.ModelProvider})
}

// UpdateCommand changes the supplied fields of an Agent. Configuration holds
// the supplied top-level fields only (see mergeConfiguration). Metadata nil
// keeps the saved map; a supplied map replaces it. ModelProvider nil keeps the
// saved bundle.
type UpdateCommand struct {
	TenantID      string
	AgentID       string
	Configuration json.RawMessage
	Metadata      *map[string]string
	ModelProvider *ModelProviderChange
}

// Update validates the supplied fields before it looks the Agent up, so an
// invalid request is rejected the same way for a missing Agent. An update that
// supplies nothing still advances UpdatedAt.
func (s *Service) Update(ctx context.Context, cmd UpdateCommand) (Agent, error) {
	patch, err := decodePatch(cmd.Configuration)
	if err != nil {
		return Agent{}, err
	}
	var metadata json.RawMessage
	if cmd.Metadata != nil {
		if metadata, err = encodeMetadata(*cmd.Metadata); err != nil {
			return Agent{}, err
		}
	}
	var updated Agent
	err = s.storage.WithAgentUpdate(ctx, cmd.TenantID, cmd.AgentID, func(tx UpdateTx) error {
		current, err := tx.LoadAgent()
		if err != nil {
			return err
		}
		revision, err := revise(current, patch, metadata, cmd.ModelProvider)
		if err != nil {
			return err
		}
		updated, err = tx.ApplyRevision(revision)
		return err
	})
	if err != nil {
		return Agent{}, err
	}
	return updated, nil
}

// revise decides an Agent's next state. metadata nil keeps the saved map.
func revise(current Agent, patch map[string]json.RawMessage, metadata json.RawMessage, change *ModelProviderChange) (Revision, error) {
	configuration, err := mergeConfiguration(current.Configuration, patch)
	if err != nil {
		return Revision{}, err
	}
	var provider *v1.ModelProviderInput
	if change != nil {
		provider = change.Provider
	}
	if err := validateModelExecution(configuration, provider); err != nil {
		return Revision{}, err
	}
	if metadata == nil {
		if metadata, err = encodeMetadata(current.Metadata); err != nil {
			return Revision{}, err
		}
	}
	return Revision{Metadata: metadata, Configuration: configuration, ModelProvider: change}, nil
}

// DeleteCommand removes an Agent. Session snapshots taken from it stay.
type DeleteCommand struct {
	TenantID string
	AgentID  string
}

// Delete returns the deleted Agent's ID.
func (s *Service) Delete(ctx context.Context, cmd DeleteCommand) (string, error) {
	return s.storage.DeleteAgent(ctx, cmd.TenantID, cmd.AgentID)
}
