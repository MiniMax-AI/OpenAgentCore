package modelconfiguration

import (
	"context"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// Service replaces, removes and resolves deployment defaults. Storage seals
// the complete bundle, key included, to its Harness.
type Service struct {
	storage Storage
}

// NewService requires storage.
func NewService(storage Storage) (*Service, error) {
	if storage == nil {
		return nil, errors.New("model configuration requires storage")
	}
	return &Service{storage: storage}, nil
}

// Replace validates the complete configuration through the Harness
// declaration and stores it under a new revision. Sessions that already froze
// a default keep theirs.
func (s *Service) Replace(ctx context.Context, replacement Replacement) (Configuration, error) {
	configuration := replacement.Configuration
	if err := configuration.ValidateHarness(replacement.Harness); err != nil {
		return Configuration{}, err
	}
	view := configuration.SafeView()
	return s.storage.Replace(ctx, Record{Harness: replacement.Harness, Provider: *view.ModelProvider, Model: view.Model, HarnessConfig: view.HarnessConfig, Configuration: configuration})
}

// Delete removes the Harness's default. It is idempotent. Sessions that
// already froze the default keep it.
func (s *Service) Delete(ctx context.Context, harness string) error {
	return s.storage.Delete(ctx, harness)
}

// Resolve opens the Harness's default for Session creation. It returns nil
// when the Harness has none, and credentialcrypto.ErrUnavailable without a
// credential key.
func (s *Service) Resolve(ctx context.Context, harness string) (*Snapshot, error) {
	bundle, err := s.storage.LoadBundle(ctx, harness)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	configuration := bundle.Configuration
	// A bundle that opens but no longer validates is not a credential failure.
	// Its stored row stays intact so the operator can inspect and replace it.
	if err := configuration.ValidateHarness(harness); err != nil {
		return nil, err
	}
	return &Snapshot{Provider: &configuration.ModelProvider, Model: configuration.Model, HarnessConfig: v1.ResolvedHarnessConfig(configuration.HarnessConfig), Revision: bundle.Revision}, nil
}
