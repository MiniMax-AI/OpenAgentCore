package sessions

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
)

// Service runs the pooled Session use cases: those that need no execution
// fencing, even when only execution calls them. Reads go through Reader.
type Service struct {
	storage Storage
	rules   *placement.Rules
}

// NewService builds the Session use cases on storage. rules admit and place
// hosted Session creation; nil rules serve a service that never creates
// hosted Sessions, whose hosted creation then fails.
func NewService(storage Storage, rules *placement.Rules) (*Service, error) {
	if storage == nil {
		return nil, errors.New("sessions: storage is required")
	}
	return &Service{storage: storage, rules: rules}, nil
}

// Storage persists the pooled Session use cases, one family per line.
type Storage interface {
	ArtifactStorage
	CreationStorage
	DeviceStorage
	ExecutorCredentialStorage
	SessionStorage
	InputStorage
}
