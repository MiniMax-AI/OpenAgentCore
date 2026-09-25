package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// modelProviderKeyPurpose separates model provider key fingerprints from any
// other use of the credential key.
const modelProviderKeyPurpose = "parsar.agents-api.model-provider-api-key.v1"

// fingerprintedProvider replaces a bundle's key with its keyed fingerprint, so
// idempotency hashes tell keys apart without ever hashing a key itself.
func (s *Store) fingerprintedProvider(provider *v1.ModelProviderInput) (*v1.ModelProviderInput, error) {
	if provider == nil {
		return nil, nil
	}
	fingerprint, err := s.credentialCipher.Fingerprint(modelProviderKeyPurpose, provider.APIKey)
	if err != nil {
		return nil, ErrCredentialStorageUnavailable
	}
	copy := *provider
	copy.APIKey = "fingerprint:" + fingerprint
	return &copy, nil
}

// withoutProviderKey replaces a caller intent's x_agents_core.model_provider.api_key
// with its keyed fingerprint. Requests without a key are returned unchanged.
func (s *Store) withoutProviderKey(raw json.RawMessage) (json.RawMessage, error) {
	var request, extension, provider map[string]json.RawMessage
	var key string
	if json.Unmarshal(raw, &request) != nil || json.Unmarshal(request["x_agents_core"], &extension) != nil ||
		json.Unmarshal(extension["model_provider"], &provider) != nil || json.Unmarshal(provider["api_key"], &key) != nil {
		return raw, nil
	}
	fingerprinted, err := s.fingerprintedProvider(&v1.ModelProviderInput{APIKey: key})
	if err != nil {
		return nil, err
	}
	provider["api_key"], _ = json.Marshal(fingerprinted.APIKey)
	extension["model_provider"], _ = json.Marshal(provider)
	request["x_agents_core"], _ = json.Marshal(extension)
	return json.Marshal(request)
}

func (s *Store) creationRequestHash(raw json.RawMessage) (pgtype.Text, error) {
	if len(raw) == 0 {
		return pgtype.Text{}, nil
	}
	if len(raw) > 16<<20 {
		return pgtype.Text{}, ErrInvalidInput
	}
	raw, err := s.withoutProviderKey(raw)
	if err != nil {
		return pgtype.Text{}, err
	}
	canonical, err := canonicalJSONObject(raw)
	if err != nil {
		return pgtype.Text{}, err
	}
	hash := sha256.Sum256(canonical)
	return pgtype.Text{String: hex.EncodeToString(hash[:]), Valid: true}, nil
}

// FindSessionCreation recovers recorded caller intent without resolving a mutable source.
func (s *Store) FindSessionCreation(ctx context.Context, tenantID, key string, request json.RawMessage, creator identity.Subject) (SessionCreation, error) {
	if err := creator.Validate(); err != nil {
		return SessionCreation{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return SessionCreation{}, err
	}
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return SessionCreation{}, ErrInvalidInput
	}
	hash, err := s.creationRequestHash(request)
	if errors.Is(err, ErrCredentialStorageUnavailable) {
		// Without the credential key no Session with a provider bundle can have
		// been committed or can be created; creation reports the missing key
		// after request validation.
		return SessionCreation{}, ErrNotFound
	}
	if err != nil {
		return SessionCreation{}, err
	}
	if !hash.Valid {
		return SessionCreation{}, ErrInvalidInput
	}
	var row sqlc.Session
	var environment *Environment
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		row, err = q.FindSessionCreation(ctx, sqlc.FindSessionCreationParams{TenantID: tenant, IdempotencyKey: key})
		if err != nil {
			return err
		}
		environment, err = sessionEnvironmentSnapshot(ctx, q, row)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionCreation{}, ErrNotFound
	}
	if err != nil {
		return SessionCreation{}, fmt.Errorf("find session creation: %w", err)
	}
	if row.DeletedAt.Valid || !row.CreatorKind.Valid || !row.CreatorID.Valid || row.CreatorKind.String != creator.Kind || row.CreatorID.String != creator.ID {
		return SessionCreation{}, ErrIdempotencyConflict
	}
	// Missing request intent does not imply missing ownership. Known creators may
	// still fall back to the original resolved-request equivalence at the upsert.
	if !row.CreationRequestHash.Valid {
		return SessionCreation{}, ErrNotFound
	}
	if row.CreationRequestHash.String != hash.String {
		return SessionCreation{}, ErrIdempotencyConflict
	}
	session, err := sessionFromRow(row)
	session.Environment = environment
	// The row and cursor share one committed snapshot; later events remain observable.
	return SessionCreation{Session: session, Cursor: row.EventSequence}, err
}
