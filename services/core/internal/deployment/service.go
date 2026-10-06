package deployment

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

// Service reads the deployment and manages its nodes. It grants no execution
// authority: deployment changes run through ExecutionOperations.
type Service struct {
	storage  Storage
	reader   Reader
	registry *providers.Registry
	// rules decide admission and placement. Their public URL is OAC_PUBLIC_URL:
	// Core reports it as the deployment and node configuration core_url and
	// records it on each node it enrolls.
	rules *placement.Rules
}

// NewService returns the deployment service. rules are the installation's
// placement rules, built once with its provider declarations and validated
// public URL.
func NewService(storage Storage, reader Reader, registry *providers.Registry, rules *placement.Rules) (*Service, error) {
	if storage == nil || reader == nil || registry == nil || rules == nil {
		return nil, errors.New("deployment service requires storage, a reader, a provider registry and placement rules")
	}
	return &Service{storage: storage, reader: reader, registry: registry, rules: rules}, nil
}

// View returns the deployment as administrators read it.
func (s *Service) View(ctx context.Context) (View, error) {
	snapshot, err := s.reader.Snapshot(ctx)
	if err != nil {
		return View{}, err
	}
	return s.view(snapshot)
}

// view reports the public URL as the deployment's read-only core_url.
func (s *Service) view(snapshot Snapshot) (View, error) {
	d := snapshot.Record
	result := View{InstallationID: d.InstallationID, Provider: d.Provider, CoreURL: s.rules.PublicURL(), OwnerEpoch: d.OwnerEpoch, Generation: d.Generation, Mode: d.Mode, Rollout: snapshot.Rollout, Resources: snapshot.Resources}
	if len(d.Specification) > 0 && string(d.Specification) != "{}" {
		var spec sandbox.DeploymentSpec
		if json.Unmarshal(d.Specification, &spec) == nil {
			result.Specification = &spec
			result.SpecificationDigest = spec.Digest(d.Provider)
		}
	}
	if d.Provider != "" {
		value, err := s.registry.Decode(d.Provider, sandbox.ConfigurationRecord{Public: d.Configuration.Public, Metadata: d.Configuration.Metadata})
		if err != nil {
			return View{}, ErrConflict
		}
		record, err := s.registry.Encode(d.Provider, value)
		if err != nil {
			return View{}, ErrConflict
		}
		result.Configuration = configurationJSON(record.Public)
		result.Metadata = configurationJSON(record.Metadata)
		result.CredentialConfigured = d.CredentialStored
		checkpoint, err := s.registry.SupportsCheckpoint(d.Provider)
		if err != nil {
			return View{}, err
		}
		if checkpoint {
			result.Suspension = &Suspension{IdleSeconds: d.IdleSeconds, RetentionSeconds: d.RetentionSeconds}
		}
	}
	if d.Reset != nil {
		result.Reset = &Reset{Clear: d.Reset.Clear, RequestedAt: d.Reset.RequestedAt, DeadlineAt: d.Reset.DeadlineAt, ForcedAt: d.Reset.ForcedAt, Remaining: snapshot.Remaining}
	}
	return result, nil
}

// Setup returns the Web-managed selection with its credential.
func (s *Service) Setup(ctx context.Context) (Setup, error) {
	d, err := s.reader.Deployment(ctx)
	if err != nil {
		return Setup{}, err
	}
	return s.setup(d)
}

func (s *Service) setup(d Record) (Setup, error) {
	if !d.WebManaged {
		return Setup{}, ErrConflict
	}
	result := Setup{InstallationID: d.InstallationID, Provider: d.Provider, BackendFingerprint: d.BackendFingerprint, Generation: d.Generation, Mode: d.Mode, AdmissionPaused: d.AdmissionPaused}
	if err := json.Unmarshal(d.Specification, &result.Specification); err != nil {
		return Setup{}, err
	}
	if d.Provider == "" {
		return result, nil
	}
	if err := s.registry.ValidateSpecification(d.Provider, result.Specification); err != nil {
		return Setup{}, err
	}
	if d.CredentialError != nil {
		return Setup{}, d.CredentialError
	}
	var err error
	// A stored configuration the provider no longer decodes is a conflict the
	// administrator resolves by saving the setup again.
	result.Configuration, err = s.registry.Decode(d.Provider, d.Configuration)
	if err != nil {
		return Setup{}, ErrConflict
	}
	return s.describe(result, d.IdleSeconds, d.RetentionSeconds)
}

// describe adds the provider's declared operations, suspension policy and
// credential use.
func (s *Service) describe(setup Setup, idleSeconds, retentionSeconds int64) (Setup, error) {
	adapter, err := s.registry.Lookup(setup.Provider)
	if err != nil {
		return Setup{}, err
	}
	setup.Operations = adapter.Operations()
	checkpoint, err := s.registry.SupportsCheckpoint(setup.Provider)
	if err != nil {
		return Setup{}, err
	}
	if checkpoint {
		setup.Suspension = &Suspension{IdleSeconds: idleSeconds, RetentionSeconds: retentionSeconds}
	}
	setup.UsesCredential, err = s.registry.UsesCredential(setup.Provider)
	if err != nil {
		return Setup{}, err
	}
	return setup, nil
}

// specification returns the deployment's specification when its provider
// still accepts it.
func (s *Service) specification(d Record) (sandbox.DeploymentSpec, error) {
	var spec sandbox.DeploymentSpec
	if json.Unmarshal(d.Specification, &spec) != nil || s.registry.ValidateSpecification(d.Provider, spec) != nil {
		return spec, ErrSpecificationMismatch
	}
	return spec, nil
}

// AllocationSetup returns the immutable generation an allocation was created
// with, configured with the current credential. A released allocation stays
// historical and is never rebound.
func (s *Service) AllocationSetup(ctx context.Context, ref sandbox.Reference) (Setup, error) {
	a, err := s.reader.Allocation(ctx, ref)
	if err != nil {
		return Setup{}, err
	}
	if a.ID != ref.AllocationID || a.Generation == 0 || a.Released {
		return Setup{}, ErrInvalidInput
	}
	if a.InstallationID != a.Deployment.InstallationID {
		return Setup{}, sandbox.ErrOwnership
	}
	current, err := s.setup(a.Deployment)
	if err != nil {
		return Setup{}, err
	}
	if a.Generation == current.Generation {
		return current, nil
	}
	g := a.Retained
	if g == nil || g.Provider != current.Provider {
		return Setup{}, ErrConflict
	}
	result := current
	result.Generation = g.Generation
	result.Specification = sandbox.DeploymentSpec{}
	if err := json.Unmarshal(g.Specification, &result.Specification); err != nil {
		return Setup{}, err
	}
	result.Configuration, err = s.registry.Decode(g.Provider, g.Configuration)
	if err != nil {
		return Setup{}, ErrConflict
	}
	if current.UsesCredential {
		composed, err := s.registry.WithCredential(result.selection(), current.selection())
		if err != nil {
			return Setup{}, ErrConflict
		}
		result.Configuration = composed.Configuration
	}
	return result, nil
}

// GenerationPage returns up to 32 retained generations after the given one,
// without their credential. Callers keep a deadline over the full scan.
func (s *Service) GenerationPage(ctx context.Context, after int64) ([]Setup, error) {
	rows, err := s.reader.Generations(ctx, after)
	if err != nil {
		return nil, err
	}
	result := make([]Setup, 0, len(rows))
	for _, r := range rows {
		v := Setup{Generation: r.Generation, Provider: r.Provider}
		if err := json.Unmarshal(r.Specification, &v.Specification); err != nil {
			return nil, err
		}
		v.Configuration, err = s.registry.Decode(r.Provider, r.Configuration)
		if err != nil {
			return nil, ErrConflict
		}
		adapter, err := s.registry.Lookup(r.Provider)
		if err != nil {
			return nil, err
		}
		v.Mode, v.Operations = adapter.Mode, adapter.Operations()
		result = append(result, v)
	}
	return result, nil
}

// WithCredential returns owner configured with the credential of candidate,
// which selects the same provider.
func (s *Service) WithCredential(owner, candidate Setup) (Setup, error) {
	selection, err := s.registry.WithCredential(owner.selection(), candidate.selection())
	if err != nil {
		return Setup{}, err
	}
	owner.Configuration = selection.Configuration
	return owner, nil
}

// DecodeConfiguration reads a submitted provider configuration and credential.
func (s *Service) DecodeConfiguration(provider string, public, credential json.RawMessage) (sandbox.Configuration, error) {
	return s.registry.DecodeInput(provider, public, credential)
}

// SetupForSelection prepares a selection's setup without writing or allocating
// resources. It rejects a provider that requires a reachable public origin
// while the installation public URL is loopback.
func (s *Service) SetupForSelection(installationID string, input sandbox.Selection) (Setup, error) {
	if _, err := parseID(installationID); err != nil {
		return Setup{}, err
	}
	normalized, err := s.registry.Normalize(input)
	if err != nil {
		return Setup{}, configurationError(err)
	}
	description, err := s.registry.Describe(input.Provider, installationID)
	if err != nil {
		return Setup{}, configurationError(err)
	}
	// Adapters declare whether their guests require a public Core origin.
	if err := s.rules.CheckPublicOrigin(input.Provider); err != nil {
		return Setup{}, err
	}
	result := Setup{InstallationID: installationID, Provider: input.Provider, Mode: description.Mode, Specification: normalized.DeploymentSpec, Configuration: normalized.Configuration, BackendFingerprint: description.BackendFingerprint}
	return s.describe(result, description.IdleSeconds, description.RetentionSeconds)
}

// validateSelection rejects a selection its provider cannot normalize.
func (s *Service) validateSelection(input sandbox.Selection) error {
	if _, err := s.registry.Normalize(input); err != nil {
		return configurationError(err)
	}
	return nil
}

// selectionEqual reports whether input selects the stored provider,
// specification and configuration.
func (s *Service) selectionEqual(d Record, input sandbox.Selection) (bool, error) {
	if d.Provider != input.Provider {
		return false, nil
	}
	previous, err := s.setup(d)
	if err != nil {
		return false, err
	}
	normalized, err := s.registry.Normalize(input)
	if err != nil {
		return false, configurationError(err)
	}
	if previous.Specification.Digest(d.Provider) != normalized.DeploymentSpec.Digest(input.Provider) {
		return false, nil
	}
	return s.registry.Equal(input.Provider, previous.Configuration, normalized.Configuration)
}
