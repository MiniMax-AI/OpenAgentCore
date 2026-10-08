package sessions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
)

// CreationStorage persists Session creation and finds an earlier one.
type CreationStorage interface {
	// FingerprintProviderKey returns the keyed fingerprint of a model
	// provider key, so retry identities tell keys apart without hashing a key.
	FingerprintProviderKey(secret string) (string, error)
	// WithCreation runs apply in one pooled transaction and commits only when
	// apply returns nil. A malformed tenant is ErrInvalidInput.
	WithCreation(ctx context.Context, tenant string, apply func(context.Context, CreationTx) error) error
	// FindCreation reads the tenant's Session created under key, deleted or
	// not, or ErrNotFound. A malformed tenant is ErrInvalidInput.
	FindCreation(ctx context.Context, tenant, key string) (CreationRecord, error)
}

// NewSession is a validated Session creation with its retry identity.
type NewSession struct {
	Key           string
	Creator       identity.Subject
	Engine        string
	Metadata      json.RawMessage
	Configuration json.RawMessage
	// RequestHash identifies the resolved request; IntentHash, when set, the
	// caller's intent before mutable sources resolved.
	RequestHash string
	IntentHash  *string
}

// CreationRecord is what a retry identity lookup compares.
type CreationRecord struct {
	SessionID string
	Deleted   bool
	// Creator is nil when the Session records no complete creator.
	Creator *identity.Subject
	// IntentHash is nil when the creation recorded no caller intent.
	IntentHash *string
}

// CreationTx is the Session creation transaction. UpsertSession binds it to
// the stored Session, which every later method acts on.
type CreationTx interface {
	InputAdmissionTx
	InputActivityTx
	// UpsertSession inserts the Session or, under the tenant and key, returns
	// the stored one with Created false when its creator and identity match:
	// the intent hash when the stored Session has one, else the request hash.
	// A deleted or mismatched one is ErrIdempotencyConflict. Cursor is the
	// Session's event sequence before anything the transaction admits.
	UpsertSession(ctx context.Context, session NewSession) (Creation, error)
	// LockDeployment locks and reads the sandbox deployment, serializing
	// hosted creation with deployment changes, restore and node removal.
	LockDeployment(ctx context.Context) (placement.Deployment, error)
	LoadNodes(ctx context.Context) ([]placement.Node, error)
	ReservePlacement(ctx context.Context, chosen placement.Placement) error
	// LockSkills locks the tenant's Skills in ID order, whatever the order of
	// ids, and returns them by ID. A missing one is ErrNotFound.
	LockSkills(ctx context.Context, ids []string) (map[string]skills.Skill, error)
	// ReadSkillVersion opens and verifies a version of a locked Skill. A
	// missing one is ErrNotFound; one that does not open or verify is an
	// internal error.
	ReadSkillVersion(ctx context.Context, skill string, version int64) (skills.Content, error)
	// SaveModelExecution seals the Session's model provider bundle.
	SaveModelExecution(ctx context.Context, provider v1.ModelProviderInput) error
	// SaveExecutionConfiguration records the frozen execution projection and,
	// unless it is uuid.Nil, the deployment provider revision it resolved.
	SaveExecutionConfiguration(ctx context.Context, projection v1.SessionExecutionConfiguration, revision uuid.UUID) error
	// SaveInitialFiles copies the validated initial files, a file_id one from
	// its locked source File, seals them under the Session and records their
	// metadata in its configuration. A missing source is ErrNotFound and one
	// beyond environmentconfig.MaxInitialFileBytes ErrInvalidInput.
	SaveInitialFiles(ctx context.Context, files []environmentconfig.InitialFile) error
	// SaveSetup seals the installed setup under the Session and records its
	// metadata in its configuration.
	SaveSetup(ctx context.Context, setup environmentconfig.Setup) error
	// CreateEnvironment creates the Session's Environment, pending
	// preparation when the Session froze a setup or initial files, and
	// returns its ID.
	CreateEnvironment(ctx context.Context) (string, error)
	// CreateInputReservation reserves the batch under key until the deadline
	// the database clock sets, marked as the Session's initial input.
	CreateInputReservation(ctx context.Context, key string, batch json.RawMessage, initial bool) (EnvironmentInputReservation, error)
	// PruneChanges bounds the Session's retained public changes.
	PruneChanges(ctx context.Context) error
	// AuditCreation records the create write audit, listing created.
	AuditCreation(ctx context.Context, created ...writeaudit.Resource) error
	// LoadSession reads the Session with its Environment and activity as the
	// transaction has left it.
	LoadSession(ctx context.Context) (Session, error)
}

// CreateSession creates a Session under a tenant-scoped key, so retries,
// including concurrent ones, return the stored Session. The same key with
// different input or another creator is ErrIdempotencyConflict. Only the new
// Session freezes resources, takes a placement and admits its initial inputs,
// so a retry after completion or later Turns never submits them again.
func (s *Service) CreateSession(ctx context.Context, tenant string, input CreateSession) (Creation, error) {
	session, batch, encoded, err := prepareCreation(input, s.storage.FingerprintProviderKey)
	if err != nil {
		return Creation{}, err
	}
	var result Creation
	err = s.storage.WithCreation(ctx, tenant, func(ctx context.Context, tx CreationTx) error {
		upserted, err := tx.UpsertSession(ctx, session)
		if err != nil {
			return err
		}
		var created []writeaudit.Resource
		if upserted.Created {
			if created, err = s.createResources(ctx, tx, upserted.Session, input, batch, encoded); err != nil {
				return err
			}
		}
		if err := tx.AuditCreation(ctx, created...); err != nil {
			return err
		}
		result = upserted
		result.Session, err = tx.LoadSession(ctx)
		return err
	})
	if err != nil {
		return Creation{}, fmt.Errorf("create session: %w", err)
	}
	return result, nil
}

// createResources freezes what the new Session keeps, creates its Environment
// and admits its initial inputs, in lock order: the hosted deployment, the
// Skills, then the Session's own rows. It returns the resources created.
func (s *Service) createResources(ctx context.Context, tx CreationTx, session Session, input CreateSession, batch []Input, encoded json.RawMessage) ([]writeaudit.Resource, error) {
	var configuration struct {
		Environment struct {
			Type string `json:"type"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(session.Configuration, &configuration); err != nil {
		return nil, err
	}
	hosted := configuration.Environment.Type == "openai_hosted"
	var deployment placement.Deployment
	if hosted {
		if s.rules == nil {
			return nil, errors.New("hosted Session creation requires placement rules")
		}
		var err error
		if deployment, err = tx.LockDeployment(ctx); err != nil {
			return nil, err
		}
		if err := s.rules.CheckAdmission(deployment, ""); err != nil {
			return nil, err
		}
	}
	setup, err := freezeSkills(ctx, tx, input.Initialization)
	if err != nil {
		return nil, err
	}
	if input.ModelProvider != nil {
		if err := tx.SaveModelExecution(ctx, *input.ModelProvider); err != nil {
			return nil, err
		}
	}
	if input.ExecutionConfiguration != nil {
		projection, revision, err := freezeProjection(session, *input.ExecutionConfiguration, input.ModelProvider, input.ModelProviderSource, input.DeploymentProviderRevision)
		if err != nil {
			return nil, err
		}
		if err := tx.SaveExecutionConfiguration(ctx, projection, revision); err != nil {
			return nil, err
		}
	}
	if len(input.InitialFiles) > 0 {
		if environmentconfig.ValidateInitialFiles(input.InitialFiles) != nil {
			return nil, ErrInvalidInput
		}
		if err := tx.SaveInitialFiles(ctx, input.InitialFiles); err != nil {
			return nil, err
		}
	}
	if !setup.Empty() {
		if err := tx.SaveSetup(ctx, setup); err != nil {
			return nil, err
		}
	}
	created := []writeaudit.Resource{{Type: writeaudit.ResourceSession, ID: session.ID}}
	creates, err := createsEnvironment(session.Configuration)
	if err != nil {
		return nil, err
	}
	environment := ""
	if creates {
		if environment, err = tx.CreateEnvironment(ctx); err != nil {
			return nil, err
		}
		created = append(created, writeaudit.Resource{Type: writeaudit.ResourceEnvironment, ID: environment, ParentID: session.ID})
	}
	if hosted {
		nodes, err := tx.LoadNodes(ctx)
		if err != nil {
			return nil, err
		}
		chosen, err := s.rules.DecidePlacement(deployment, nodes)
		if err != nil {
			return nil, err
		}
		if chosen != nil {
			if err := tx.ReservePlacement(ctx, *chosen); err != nil {
				return nil, err
			}
		}
	}
	if len(batch) == 0 {
		return created, nil
	}
	// The input key is internal, independent of the creation key and of keys
	// callers send to the events endpoint.
	key := uuid.NewString()
	if environment != "" {
		// Environment input waits for preparation and the leased promotion.
		err = TrackInputActivity(ctx, tx, func(ctx context.Context) error {
			_, err := tx.CreateInputReservation(ctx, key, encoded, true)
			return err
		})
	} else {
		_, err = AdmitInputs(ctx, tx, key, batch)
	}
	if err != nil {
		return nil, err
	}
	return created, tx.PruneChanges(ctx)
}

// freezeSkills replaces each Skill reference of setup with the version it
// selects, read under the Skill locks, so the result no longer depends on any
// Skill. The locks serialize selection with default changes and version and
// Skill deletion.
func freezeSkills(ctx context.Context, tx CreationTx, setup environmentconfig.Setup) (environmentconfig.Setup, error) {
	if setup.Validate() != nil {
		return environmentconfig.Setup{}, ErrInvalidInput
	}
	var ids []string
	for _, skill := range setup.Skills {
		if skill.Metadata.Type == "skill_reference" {
			ids = append(ids, skill.Metadata.SkillID)
		}
	}
	if len(ids) > 0 {
		owners, err := tx.LockSkills(ctx, ids)
		if err != nil {
			return environmentconfig.Setup{}, err
		}
		setup.Skills = slices.Clone(setup.Skills)
		for i, skill := range setup.Skills {
			if skill.Metadata.Type != "skill_reference" {
				continue
			}
			owner := owners[skill.Metadata.SkillID]
			number, err := skills.SelectVersion(skill.Metadata.Version, owner.DefaultVersion, owner.LatestVersion)
			if err != nil {
				return environmentconfig.Setup{}, ErrInvalidInput
			}
			content, err := tx.ReadSkillVersion(ctx, skill.Metadata.SkillID, number)
			if err != nil {
				return environmentconfig.Setup{}, err
			}
			version := content.Version
			setup.Skills[i] = environmentconfig.Skill{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: version.SkillID, Version: strconv.FormatInt(version.Version, 10), Name: version.Name, Description: version.Description}, Archive: content.Archive}
		}
	}
	if setup.ValidateInstalled() != nil {
		return environmentconfig.Setup{}, ErrInvalidInput
	}
	return setup, nil
}

// freezeProjection checks the execution projection against the new Session
// and its provider bundle and completes it as stored. It also returns the
// deployment provider revision to record, uuid.Nil for none. The projection is
// creation metadata, never retry identity.
func freezeProjection(session Session, frozen v1.SessionExecutionConfiguration, provider *v1.ModelProviderInput, providerSource v1.ExecutionSource, revision uuid.UUID) (v1.SessionExecutionConfiguration, uuid.UUID, error) {
	model, err := ExecutionModel(session.Configuration)
	if err != nil {
		return v1.SessionExecutionConfiguration{}, uuid.Nil, err
	}
	if !sameExecutionValue(frozen.Model.Value, model) || frozen.Harness.Value == nil || *frozen.Harness.Value != session.Engine ||
		!validExecutionSource(frozen.Model.Source) || !validExecutionSource(frozen.Harness.Source) {
		return v1.SessionExecutionConfiguration{}, uuid.Nil, fmt.Errorf("%w: execution projection does not match Session configuration", ErrInvalidInput)
	}
	recorded := uuid.Nil
	switch frozen.ModelProvider.Source {
	case v1.ExecutionSourceDeployment:
		if provider == nil {
			return v1.SessionExecutionConfiguration{}, uuid.Nil, fmt.Errorf("%w: execution projection has no model provider", ErrInvalidInput)
		}
		// The deployment default is readable with the same Core key, so the
		// safe view is recorded from the frozen bundle itself. Native options
		// are never part of it.
		frozen.ModelProvider.Status = v1.ExecutionProviderAvailable
		frozen.ModelProvider.Configuration = provider.SafeView()
		if providerSource == v1.ExecutionSourceDeployment {
			recorded = revision
		}
	case v1.ExecutionSourceSession, v1.ExecutionSourceAgent:
		if provider == nil || frozen.ModelProvider.Status != v1.ExecutionProviderAvailable || frozen.ModelProvider.Configuration == nil || *frozen.ModelProvider.Configuration != *provider.SafeView() {
			return v1.SessionExecutionConfiguration{}, uuid.Nil, fmt.Errorf("%w: execution projection does not match model provider", ErrInvalidInput)
		}
	case v1.ExecutionSourceUnknown:
		frozen.ModelProvider.Status = v1.ExecutionProviderUnavailable
		frozen.ModelProvider.Configuration = nil
	default:
		return v1.SessionExecutionConfiguration{}, uuid.Nil, fmt.Errorf("%w: invalid execution projection source", ErrInvalidInput)
	}
	NormalizeExecutionProjection(&frozen, session.ID)
	return frozen, recorded, nil
}

func sameExecutionValue(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func validExecutionSource(source v1.ExecutionSource) bool {
	switch source {
	case v1.ExecutionSourceSession, v1.ExecutionSourceAgent, v1.ExecutionSourceDeployment, v1.ExecutionSourceUnknown:
		return true
	}
	return false
}

// prepareCreation validates input and derives the Session to upsert with its
// retry identity, and the validated initial inputs with their encoding.
func prepareCreation(input CreateSession, fingerprint func(string) (string, error)) (NewSession, []Input, json.RawMessage, error) {
	if err := input.Creator.Validate(); err != nil {
		return NewSession{}, nil, nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	engine := strings.TrimSpace(input.Engine)
	if !ValidEngine(engine) || !validCreationKey(input.IdempotencyKey) {
		return NewSession{}, nil, nil, fmt.Errorf("%w: engine and idempotency key are required", ErrInvalidInput)
	}
	labels := input.Metadata
	if labels == nil {
		labels = map[string]string{}
	}
	encodedMetadata, err := metadata.Encode(labels)
	if err != nil {
		return NewSession{}, nil, nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if len(input.Configuration) > 512*1024 {
		return NewSession{}, nil, nil, fmt.Errorf("%w: configuration exceeds 512 KiB", ErrInvalidInput)
	}
	configuration, err := jsonobject.Normalize(input.Configuration)
	if err != nil {
		return NewSession{}, nil, nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	var batch []Input
	var encodedInput json.RawMessage
	if len(input.InitialInputs) > 0 {
		if batch, encodedInput, err = validateMessageInputs(input.InitialInputs); err != nil {
			return NewSession{}, nil, nil, err
		}
	}
	if input.ModelProvider != nil {
		if err := input.ModelProvider.ValidateHarness(engine); err != nil {
			return NewSession{}, nil, nil, fmt.Errorf("%w: %s", ErrInvalidInput, err)
		}
	}
	var initialization *environmentconfig.Setup
	if !input.Initialization.Empty() {
		initialization = &input.Initialization
	}
	// The retry identity carries a caller's provider key only as a keyed
	// fingerprint. A deployment default is not caller input: leaving it out
	// keeps retries equivalent when the default is set, replaced or removed.
	var fingerprinted *v1.ModelProviderInput
	if input.ModelProviderSource != v1.ExecutionSourceDeployment {
		if fingerprinted, err = fingerprintedProvider(input.ModelProvider, fingerprint); err != nil {
			return NewSession{}, nil, nil, err
		}
	}
	// encoding/json sorts map keys, so key order does not affect retries.
	canonical, err := json.Marshal(struct {
		ModelProvider  *v1.ModelProviderInput `json:",omitempty"`
		Engine         string
		Metadata       map[string]string
		Configuration  json.RawMessage                 `json:",omitempty"`
		InitialInputs  json.RawMessage                 `json:",omitempty"`
		InitialFiles   []environmentconfig.InitialFile `json:",omitempty"`
		Initialization *environmentconfig.Setup        `json:",omitempty"`
	}{fingerprinted, engine, labels, configuration, encodedInput, input.InitialFiles, initialization})
	if err != nil {
		return NewSession{}, nil, nil, fmt.Errorf("%w: input: %v", ErrInvalidInput, err)
	}
	intent, err := intentHash(input.CreationRequest, fingerprint)
	if err != nil {
		return NewSession{}, nil, nil, err
	}
	hash := sha256.Sum256(canonical)
	return NewSession{
		Key: input.IdempotencyKey, Creator: input.Creator, Engine: engine, Metadata: encodedMetadata,
		Configuration: configuration, RequestHash: hex.EncodeToString(hash[:]), IntentHash: intent,
	}, batch, encodedInput, nil
}

func validCreationKey(key string) bool {
	return strings.TrimSpace(key) != "" && len(key) <= 128
}

// fingerprintedProvider replaces a bundle's key with its keyed fingerprint.
func fingerprintedProvider(provider *v1.ModelProviderInput, fingerprint func(string) (string, error)) (*v1.ModelProviderInput, error) {
	if provider == nil {
		return nil, nil
	}
	value, err := fingerprint(provider.APIKey)
	if err != nil {
		return nil, err
	}
	copy := *provider
	copy.APIKey = "fingerprint:" + value
	return &copy, nil
}

// intentHash hashes the caller's creation intent, nil when there is none.
func intentHash(raw json.RawMessage, fingerprint func(string) (string, error)) (*string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > 16<<20 {
		return nil, ErrInvalidInput
	}
	raw, err := withoutProviderKey(raw, fingerprint)
	if err != nil {
		return nil, err
	}
	canonical, err := jsonobject.Normalize(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	hash := sha256.Sum256(canonical)
	value := hex.EncodeToString(hash[:])
	return &value, nil
}

// withoutProviderKey replaces a caller intent's x_agents_core.model_provider
// with its typed value carrying a keyed fingerprint instead of the key. It
// fails closed: an intent whose extension or provider cannot be read is
// rejected rather than hashed as raw bytes that could carry a key.
func withoutProviderKey(raw json.RawMessage, fingerprint func(string) (string, error)) (json.RawMessage, error) {
	var request map[string]json.RawMessage
	if json.Unmarshal(raw, &request) != nil || request == nil {
		return nil, ErrInvalidInput
	}
	extensionRaw, present := request["x_agents_core"]
	if !present || jsonNull(extensionRaw) {
		return raw, nil
	}
	var extension map[string]json.RawMessage
	if json.Unmarshal(extensionRaw, &extension) != nil || extension == nil {
		return nil, ErrInvalidInput
	}
	providerRaw, present := extension["model_provider"]
	if !present || jsonNull(providerRaw) {
		return raw, nil
	}
	var provider v1.ModelProviderInput
	if json.Unmarshal(providerRaw, &provider) != nil {
		return nil, ErrInvalidInput
	}
	fingerprinted, err := fingerprintedProvider(&provider, fingerprint)
	if err != nil {
		return nil, err
	}
	if extension["model_provider"], err = json.Marshal(fingerprinted); err != nil {
		return nil, err
	}
	if request["x_agents_core"], err = json.Marshal(extension); err != nil {
		return nil, err
	}
	return json.Marshal(request)
}

func jsonNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// FindSessionCreation recovers a creation by its recorded caller intent,
// without resolving a mutable source. It returns only the Session ID; a
// recovered creation is never Created.
func (s *Service) FindSessionCreation(ctx context.Context, tenant, key string, request json.RawMessage, creator identity.Subject) (Creation, error) {
	if err := creator.Validate(); err != nil {
		return Creation{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if !validCreationKey(key) {
		return Creation{}, ErrInvalidInput
	}
	hash, err := intentHash(request, s.storage.FingerprintProviderKey)
	if err != nil {
		return Creation{}, err
	}
	if hash == nil {
		return Creation{}, ErrInvalidInput
	}
	record, err := s.storage.FindCreation(ctx, tenant, key)
	if err != nil {
		return Creation{}, fmt.Errorf("find session creation: %w", err)
	}
	if err := decideRecovery(record, creator, *hash); err != nil {
		return Creation{}, err
	}
	return Creation{Session: Session{ID: record.SessionID}}, nil
}

// decideRecovery accepts a recorded creation for the creator's intent. A
// deleted Session, or one another or no complete creator made, conflicts
// before a missing intent is ErrNotFound: missing intent does not imply
// missing ownership, and the creation can still fall back to resolved-request
// equivalence at the upsert.
func decideRecovery(record CreationRecord, creator identity.Subject, hash string) error {
	if record.Deleted || record.Creator == nil || *record.Creator != creator {
		return ErrIdempotencyConflict
	}
	if record.IntentHash == nil {
		return ErrNotFound
	}
	if *record.IntentHash != hash {
		return ErrIdempotencyConflict
	}
	return nil
}
