package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/filepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/placementpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/skillpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// modelProviderKeyPurpose keys the provider-key fingerprint in creation
// identities. Changing it changes every stored identity.
const modelProviderKeyPurpose = "oac.model-provider-api-key.v1"

var _ sessions.CreationTx = (*creationTx)(nil)

func (s *Store) FingerprintProviderKey(secret string) (string, error) {
	return s.cipher.Fingerprint(modelProviderKeyPurpose, secret)
}

func (s *Store) WithCreation(ctx context.Context, tenantID string, apply func(context.Context, sessions.CreationTx) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	return s.units.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return apply(ctx, &creationTx{SessionTx: SessionTx{q: sqlc.New(tx), tenant: tenant}, tenantID: tenantID, tx: tx, cipher: s.cipher})
	})
}

func (s *Store) FindCreation(ctx context.Context, tenantID, key string) (sessions.CreationRecord, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.CreationRecord{}, err
	}
	row, err := s.units.Queries().FindSessionCreation(ctx, sqlc.FindSessionCreationParams{TenantID: tenant, IdempotencyKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.CreationRecord{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.CreationRecord{}, err
	}
	record := sessions.CreationRecord{SessionID: uuid.UUID(row.ID.Bytes).String(), Deleted: row.DeletedAt.Valid}
	if row.CreatorKind.Valid && row.CreatorID.Valid {
		record.Creator = &identity.Subject{Kind: row.CreatorKind.String, ID: row.CreatorID.String}
	}
	if row.CreationRequestHash.Valid {
		record.IntentHash = &row.CreationRequestHash.String
	}
	return record, nil
}

// creationTx is a Session creation transaction. UpsertSession binds the
// embedded SessionTx to the stored Session. tenantID is the tenant as the
// caller named it, which binds the sealed model execution and the audit.
type creationTx struct {
	SessionTx
	// environment is the Session's Environment once CreateEnvironment ran.
	environment pgtype.UUID
	tenantID    string
	tx          pgx.Tx
	cipher      *credentialcrypto.Cipher
}

func (t *creationTx) UpsertSession(ctx context.Context, session sessions.NewSession) (sessions.Creation, error) {
	stored, _, err := sessions.SplitEnvironment(session.Configuration)
	if err != nil {
		return sessions.Creation{}, err
	}
	params := sqlc.CreateSessionParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: t.tenant, Engine: session.Engine,
		Metadata: session.Metadata, IdempotencyKey: session.Key, RequestHash: session.RequestHash, Configuration: stored,
		CreatorKind: pgtype.Text{String: session.Creator.Kind, Valid: true}, CreatorID: pgtype.Text{String: session.Creator.ID, Valid: true},
	}
	if session.IntentHash != nil {
		params.CreationRequestHash = pgtype.Text{String: *session.IntentHash, Valid: true}
	}
	row, err := t.q.CreateSession(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Creation{}, sessions.ErrIdempotencyConflict
	}
	if err != nil {
		return sessions.Creation{}, storable(err)
	}
	t.session = row.ID
	created, err := sessionFromRow(row)
	return sessions.Creation{Session: created, Created: row.ID == params.ID, Cursor: row.EventSequence}, err
}

func (t *creationTx) LockDeployment(ctx context.Context) (placement.Deployment, error) {
	return placementpg.LockDeployment(ctx, t.q)
}

func (t *creationTx) LockSkills(ctx context.Context, ids []string) (map[string]skills.Skill, error) {
	locked, err := skillpg.LockSkills(ctx, t.q, t.tenant, ids)
	return locked, skillError(err)
}

func (t *creationTx) ReadSkillVersion(ctx context.Context, skill string, version int64) (skills.Content, error) {
	content, err := skillpg.ReadVersionForFreeze(ctx, t.q, t.cipher, t.tenant, skill, version)
	return content, skillError(err)
}

// skillError translates a missing Skill or version into the Session error.
func skillError(err error) error {
	if errors.Is(err, skills.ErrNotFound) {
		return sessions.ErrNotFound
	}
	return err
}

func (t *creationTx) SaveModelExecution(ctx context.Context, provider v1.ModelProviderInput) error {
	raw, err := json.Marshal(provider)
	if err != nil {
		return err
	}
	encrypted, err := t.cipher.SealModelExecution(raw, t.tenantID, optionalID(t.session))
	if err != nil {
		return fmt.Errorf("seal session model execution: %w", err)
	}
	return t.q.SaveSessionModelExecution(ctx, sqlc.SaveSessionModelExecutionParams{SessionID: t.session, EncryptedConfig: encrypted})
}

func (t *creationTx) SaveExecutionConfiguration(ctx context.Context, projection v1.SessionExecutionConfiguration, revision uuid.UUID) error {
	configuration, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	params := sqlc.SaveSessionExecutionConfigurationParams{SessionID: t.session, Configuration: configuration}
	if revision != uuid.Nil {
		params.DeploymentProviderRevision = pgtype.UUID{Bytes: revision, Valid: true}
	}
	return storable(t.q.SaveSessionExecutionConfiguration(ctx, params))
}

func (t *creationTx) SaveInitialFiles(ctx context.Context, initial []environmentconfig.InitialFile) error {
	metadata := environmentconfig.InitialFilesMetadata(initial)
	for i, f := range initial {
		body := f.Data
		if f.Type == "file_id" {
			var err error
			body, err = filepg.ReadSourceForCopy(ctx, t.tx, t.tenant, f.FileID, environmentconfig.MaxInitialFileBytes)
			switch {
			case errors.Is(err, files.ErrNotFound):
				return sessions.ErrNotFound
			case errors.Is(err, files.ErrTooLarge):
				return sessions.ErrInvalidInput
			case err != nil:
				return err
			}
		}
		id := uuid.New()
		size := int64(len(body))
		metadata[i].ID = id.String()
		metadata[i].SizeBytes = &size
		encrypted, err := t.cipher.SealEnvironmentFile(body, fileBinding(t.tenant, t.environment, id.String()))
		if err != nil {
			return fmt.Errorf("seal initial file: %w", err)
		}
		if err := t.q.CreateInitialEnvironmentFile(ctx, sqlc.CreateInitialEnvironmentFileParams{
			ID: pgtype.UUID{Bytes: id, Valid: true}, EnvironmentID: t.environment, Position: int32(i), Path: f.Path, SizeBytes: size, Contents: encrypted,
		}); err != nil {
			return storable(err)
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return storable(t.q.SetEnvironmentInitialFileMetadata(ctx, sqlc.SetEnvironmentInitialFileMetadataParams{ID: t.environment, Column2: encoded}))
}

func (t *creationTx) SaveSetup(ctx context.Context, setup environmentconfig.Setup) error {
	plaintext, err := json.Marshal(setup)
	if err != nil {
		return err
	}
	encrypted, err := t.cipher.SealEnvironmentSetup(plaintext, setupBinding(t.tenant, t.environment))
	if err != nil {
		return fmt.Errorf("seal environment setup: %w", err)
	}
	if err := t.q.CreateEnvironmentSetup(ctx, sqlc.CreateEnvironmentSetupParams{EnvironmentID: t.environment, Contents: encrypted}); err != nil {
		return err
	}
	params := sqlc.SetEnvironmentSetupMetadataParams{ID: t.environment}
	for _, field := range []struct {
		target *[]byte
		value  any
	}{
		{&params.Packages, setup.PackageMetadata()},
		{&params.Skills, setup.SkillMetadata()},
		{&params.Plugins, setup.PluginMetadata()},
		{&params.CapabilityDirectories, append([]string{}, setup.CapabilityDirectories...)},
	} {
		if *field.target, err = json.Marshal(field.value); err != nil {
			return err
		}
	}
	return storable(t.q.SetEnvironmentSetupMetadata(ctx, params))
}

func (t *creationTx) CreateEnvironment(ctx context.Context, configuration json.RawMessage, prepared bool) (string, error) {
	if err := t.insertEnvironment(ctx, sqlc.CreateEnvironmentParams{Configuration: configuration, Prepared: prepared}); err != nil {
		return "", err
	}
	if err := t.q.AttachSessionEnvironment(ctx, sqlc.AttachSessionEnvironmentParams{ID: t.session, EnvironmentID: t.environment}); err != nil {
		return "", err
	}
	return optionalID(t.environment), nil
}

// insertEnvironment inserts the Environment with a new ID, which the later
// preparation steps act on.
func (t *creationTx) insertEnvironment(ctx context.Context, params sqlc.CreateEnvironmentParams) error {
	t.environment = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	params.ID, params.TenantID = t.environment, t.tenant
	return t.q.CreateEnvironment(ctx, params)
}

func (t *creationTx) PruneChanges(ctx context.Context) error {
	return PruneChanges(ctx, t.q, t.session)
}

func (t *creationTx) AuditCreation(ctx context.Context, created ...writeaudit.Resource) error {
	return auditpg.RecordWriteAudit(ctx, t.q, t.tenantID, writeaudit.ActionCreate, writeaudit.ResourceSession, optionalID(t.session), "", created...)
}

func (t *creationTx) LoadSession(ctx context.Context) (sessions.Session, error) {
	session, _, err := loadSession(ctx, t.q, t.tenant, t.session)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Session{}, sessions.ErrNotFound
	}
	return session, err
}
