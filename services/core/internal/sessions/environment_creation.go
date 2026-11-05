package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// EnvironmentCreationTx is the part of a creation transaction that creates and
// prepares an Environment, shared by Session creation and standalone
// Environment creation.
type EnvironmentCreationTx interface {
	// LockDeployment locks and reads the sandbox deployment, serializing
	// hosted creation with deployment changes, restore and node removal.
	LockDeployment(ctx context.Context) (placement.Deployment, error)
	// LockSkills locks the tenant's Skills in ID order, whatever the order of
	// ids, and returns them by ID. A missing one is ErrNotFound.
	LockSkills(ctx context.Context, ids []string) (map[string]skills.Skill, error)
	// ReadSkillVersion opens and verifies a version of a locked Skill. A
	// missing one is ErrNotFound; one that does not open or verify is an
	// internal error.
	ReadSkillVersion(ctx context.Context, skill string, version int64) (skills.Content, error)
	// CreateEnvironment creates the Environment with the configuration,
	// pending preparation when prepared, and returns its ID. A Session's
	// Environment is attached to the Session. It precedes the setup and
	// initial files, which are saved for it.
	CreateEnvironment(ctx context.Context, configuration json.RawMessage, prepared bool) (string, error)
	// SaveInitialFiles copies the validated initial files, a file_id one from
	// its locked source File, seals them under the Environment and records
	// their metadata in its configuration. A missing source is ErrNotFound and
	// one beyond environmentconfig.MaxInitialFileBytes ErrInvalidInput.
	SaveInitialFiles(ctx context.Context, files []environmentconfig.InitialFile) error
	// SaveSetup seals the installed setup under the Environment and records
	// its metadata in its configuration.
	SaveSetup(ctx context.Context, setup environmentconfig.Setup) error
}

// environmentPreparation is what creating an Environment freezes.
type environmentPreparation struct {
	Hosted         bool
	Configuration  json.RawMessage
	Initialization environmentconfig.Setup
	InitialFiles   []environmentconfig.InitialFile
}

// admitHosted locks the sandbox deployment and checks that it admits new
// hosted work, which a pending reset or a closed deployment refuses.
func (s *Service) admitHosted(ctx context.Context, tx EnvironmentCreationTx) (placement.Deployment, error) {
	if s.rules == nil {
		return placement.Deployment{}, errors.New("hosted Environment admission requires placement rules")
	}
	deployment, err := tx.LockDeployment(ctx)
	if err != nil {
		return deployment, err
	}
	return deployment, s.rules.CheckAdmission(deployment, "")
}

// createEnvironment is the one path that creates and prepares an Environment,
// in lock order: the hosted deployment, then the Skills. It returns the
// Environment ID and the locked deployment, empty unless hosted.
func (s *Service) createEnvironment(ctx context.Context, tx EnvironmentCreationTx, in environmentPreparation) (string, placement.Deployment, error) {
	var deployment placement.Deployment
	if in.Hosted {
		var err error
		if deployment, err = s.admitHosted(ctx, tx); err != nil {
			return "", deployment, err
		}
	}
	setup, err := freezeSkills(ctx, tx, in.Initialization)
	if err != nil {
		return "", deployment, err
	}
	environment, err := tx.CreateEnvironment(ctx, in.Configuration, in.Hosted || len(in.InitialFiles) > 0 || !setup.Empty())
	if err != nil {
		return "", deployment, err
	}
	if len(in.InitialFiles) > 0 {
		if environmentconfig.ValidateInitialFiles(in.InitialFiles) != nil {
			return "", deployment, ErrInvalidInput
		}
		if err := tx.SaveInitialFiles(ctx, in.InitialFiles); err != nil {
			return "", deployment, err
		}
	}
	if !setup.Empty() {
		if err := tx.SaveSetup(ctx, setup); err != nil {
			return "", deployment, err
		}
	}
	if in.Hosted {
		if err := s.rules.CheckPublicOrigin(deployment.Provider); err != nil {
			return "", deployment, err
		}
	}
	return environment, deployment, nil
}

// EnvironmentCreationStorage persists standalone Environment creation.
type EnvironmentCreationStorage interface {
	// WithEnvironmentCreation runs apply in one pooled transaction that
	// serializes creators of the identity's key and commits only when apply
	// returns nil. CreateEnvironment stores the identity with the standalone
	// Environment. A malformed tenant is ErrInvalidInput.
	WithEnvironmentCreation(ctx context.Context, tenant string, identity EnvironmentIdentity, apply func(context.Context, StandaloneCreationTx) error) error
}

// EnvironmentIdentity is the retry identity of a standalone Environment.
type EnvironmentIdentity struct {
	Key     string
	Hash    string
	Creator identity.Subject
}

// EnvironmentCreationRecord is a stored standalone creation.
type EnvironmentCreationRecord struct {
	EnvironmentID string
	EnvironmentIdentity
}

// StandaloneCreationTx is the standalone Environment creation transaction.
type StandaloneCreationTx interface {
	EnvironmentCreationTx
	// FindCreation reads the Environment stored under the transaction's
	// identity key, or ErrNotFound. The transaction holds the key until it
	// ends, so it either finds the stored one or is the only creator.
	FindCreation(ctx context.Context) (EnvironmentCreationRecord, error)
	// AuditCreation records the create write audit of the Environment,
	// listing created.
	AuditCreation(ctx context.Context, environment string, created ...writeaudit.Resource) error
}

// CreateEnvironment is a validated standalone Environment creation.
type CreateEnvironment struct {
	Creator        identity.Subject
	IdempotencyKey string
	// CreationRequest is the caller's request, the retry identity.
	CreationRequest json.RawMessage
	Configuration   json.RawMessage
	Initialization  environmentconfig.Setup
	InitialFiles    []environmentconfig.InitialFile
}

func (s *Service) environmentIdentity(key string, creator identity.Subject, request json.RawMessage) (EnvironmentIdentity, error) {
	if err := creator.Validate(); err != nil {
		return EnvironmentIdentity{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if strings.TrimSpace(key) == "" || len(key) > 256 {
		return EnvironmentIdentity{}, ErrInvalidInput
	}
	hash, err := intentHash(request, s.storage.FingerprintProviderKey)
	if err != nil {
		return EnvironmentIdentity{}, err
	}
	if hash == nil {
		return EnvironmentIdentity{}, ErrInvalidInput
	}
	return EnvironmentIdentity{Key: key, Hash: *hash, Creator: creator}, nil
}

// decide accepts a stored creation for the identity or reports a conflict.
func (i EnvironmentIdentity) decide(record EnvironmentCreationRecord) error {
	if record.EnvironmentIdentity != i {
		return ErrIdempotencyConflict
	}
	return nil
}

// ReplayEnvironment returns the Environment already stored under the key, if
// any, under the creator's key for the same request. It runs before the request's
// references resolve, so a retry compares the request as sent.
func (s *Service) ReplayEnvironment(ctx context.Context, tenant string, creator identity.Subject, key string, request json.RawMessage) (string, bool, error) {
	ident, err := s.environmentIdentity(key, creator, request)
	if err != nil {
		return "", false, err
	}
	var environment string
	err = s.storage.WithEnvironmentCreation(ctx, tenant, ident, func(ctx context.Context, tx StandaloneCreationTx) error {
		record, err := tx.FindCreation(ctx)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ident.decide(record); err != nil {
			return err
		}
		environment = record.EnvironmentID
		return nil
	})
	return environment, environment != "", err
}

// CreateEnvironment creates a standalone Environment under a key scoped to the
// Project and creator, so retries, including concurrent ones, return the stored
// Environment. The same key with another request is ErrIdempotencyConflict.
func (s *Service) CreateEnvironment(ctx context.Context, tenant string, input CreateEnvironment) (string, error) {
	ident, err := s.environmentIdentity(input.IdempotencyKey, input.Creator, input.CreationRequest)
	if err != nil {
		return "", err
	}
	if len(input.Configuration) > 512*1024 {
		return "", fmt.Errorf("%w: configuration exceeds 512 KiB", ErrInvalidInput)
	}
	configuration, err := jsonobject.Normalize(input.Configuration)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	var environment string
	err = s.storage.WithEnvironmentCreation(ctx, tenant, ident, func(ctx context.Context, tx StandaloneCreationTx) error {
		var created []writeaudit.Resource
		record, err := tx.FindCreation(ctx)
		switch {
		case err == nil:
			if err := ident.decide(record); err != nil {
				return err
			}
			environment = record.EnvironmentID
		case errors.Is(err, ErrNotFound):
			if environment, _, err = s.createEnvironment(ctx, tx, environmentPreparation{Hosted: true, Configuration: configuration, Initialization: input.Initialization, InitialFiles: input.InitialFiles}); err != nil {
				return err
			}
			created = []writeaudit.Resource{{Type: writeaudit.ResourceEnvironment, ID: environment}}
		default:
			return err
		}
		return tx.AuditCreation(ctx, environment, created...)
	})
	if err != nil {
		return "", fmt.Errorf("create environment: %w", err)
	}
	return environment, nil
}
