package modelconfiguration

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
)

// Service replaces, removes and resolves deployment defaults. The complete
// bundle, key included, is sealed to its Harness before it reaches storage.
type Service struct {
	storage Storage
	cipher  *credentialcrypto.Cipher
}

// NewService requires storage. A nil cipher means credential encryption is not
// configured: Replace and Resolve then return credentialcrypto.ErrUnavailable.
func NewService(storage Storage, cipher *credentialcrypto.Cipher) (*Service, error) {
	if storage == nil {
		return nil, errors.New("model configuration requires storage")
	}
	return &Service{storage: storage, cipher: cipher}, nil
}

// Replace validates the complete configuration through the Harness
// declaration, seals it and stores it under a new revision. Sessions that
// already froze a default keep theirs.
func (s *Service) Replace(ctx context.Context, replacement Replacement) (Configuration, error) {
	configuration := replacement.Configuration
	if err := configuration.ValidateHarness(replacement.Harness); err != nil {
		return Configuration{}, err
	}
	raw, err := json.Marshal(configuration)
	if err != nil {
		return Configuration{}, err
	}
	sealed, err := s.cipher.SealDeploymentModelProvider(raw, replacement.Harness)
	if err != nil {
		return Configuration{}, credentialcrypto.ErrUnavailable
	}
	view := configuration.SafeView()
	return s.storage.Replace(ctx, Record{Harness: replacement.Harness, Provider: *view.ModelProvider, Model: view.Model, HarnessConfig: view.HarnessConfig, Sealed: sealed})
}

// Delete removes the Harness's default. It is idempotent. Sessions that
// already froze the default keep it.
func (s *Service) Delete(ctx context.Context, harness string) error {
	return s.storage.Delete(ctx, harness)
}

// Resolve opens the Harness's default for Session creation. It returns nil
// when the Harness has none, and credentialcrypto.ErrUnavailable when the
// bundle does not open.
func (s *Service) Resolve(ctx context.Context, harness string) (*Snapshot, error) {
	sealed, err := s.storage.LoadSealed(ctx, harness)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := s.cipher.OpenDeploymentModelProvider(sealed.Bundle, harness)
	if err != nil {
		return nil, credentialcrypto.ErrUnavailable
	}
	var configuration v1.ModelConfigurationInput
	if json.Unmarshal(raw, &configuration) != nil {
		return nil, credentialcrypto.ErrUnavailable
	}
	// A bundle that opens but no longer validates is not a credential failure.
	// Its stored row stays intact so the operator can inspect and replace it.
	if err := configuration.ValidateHarness(harness); err != nil {
		return nil, err
	}
	return &Snapshot{Provider: &configuration.ModelProvider, Model: configuration.Model, HarnessConfig: v1.ResolvedHarnessConfig(configuration.HarnessConfig), Revision: sealed.Revision}, nil
}
