package sessions

import "errors"

// Service runs the pooled Session use cases: those that need no execution
// fencing, even when only execution calls them. Reads go through Reader.
type Service struct{ storage Storage }

// NewService builds the Session use cases on storage.
func NewService(storage Storage) (*Service, error) {
	if storage == nil {
		return nil, errors.New("sessions: storage is required")
	}
	return &Service{storage: storage}, nil
}

// Storage persists the pooled Session use cases, one family per line.
type Storage interface {
	ArtifactStorage
	DeviceStorage
	ExecutorCredentialStorage
}
