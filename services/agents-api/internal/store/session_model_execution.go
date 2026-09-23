package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// The embedded provider preserves the original flat ciphertext format. Native
// options are private deployment defaults and never enter public configuration.
type sessionModelExecution struct {
	v1.ModelProviderInput
	NativeOptions map[string]any `json:"native_options,omitempty"`
}

func (s *Store) saveSessionModelExecution(ctx context.Context, q *sqlc.Queries, tenant string, session pgtype.UUID, provider *v1.ModelProviderInput, options map[string]any) error {
	if provider == nil {
		return nil
	}
	raw, err := json.Marshal(sessionModelExecution{ModelProviderInput: *provider, NativeOptions: options})
	if err != nil {
		return err
	}
	encrypted, err := s.credentialCipher.SealModelExecution(raw, tenant, uuid.UUID(session.Bytes).String())
	if err != nil {
		return ErrCredentialStorageUnavailable
	}
	return q.SaveSessionModelExecution(ctx, sqlc.SaveSessionModelExecutionParams{SessionID: session, EncryptedConfig: encrypted})
}

func (s *Store) SessionModelExecution(ctx context.Context, tenant, session string) (*v1.ModelProviderInput, error) {
	provider, _, err := s.SessionModelExecutionWithOptions(ctx, tenant, session)
	return provider, err
}

func (s *Store) SessionModelExecutionWithOptions(ctx context.Context, tenant, session string) (*v1.ModelProviderInput, map[string]any, error) {
	tenantID, err := parseID(tenant)
	if err != nil {
		return nil, nil, err
	}
	sessionID, err := parseID(session)
	if err != nil {
		return nil, nil, err
	}
	ciphertext, err := s.queries.GetSessionModelExecution(ctx, sqlc.GetSessionModelExecutionParams{TenantID: tenantID, SessionID: sessionID})
	if err != nil {
		return nil, nil, errors.New("session model execution configuration is unavailable")
	}
	raw, err := s.credentialCipher.OpenModelExecution(ciphertext, tenant, session)
	if err != nil {
		return nil, nil, errors.New("session model execution decryption is unavailable")
	}
	var frozen sessionModelExecution
	if json.Unmarshal(raw, &frozen) != nil {
		return nil, nil, errors.New("invalid stored model execution configuration")
	}
	if err := frozen.ModelProviderInput.Validate(); err != nil {
		return nil, nil, err
	}
	return &frozen.ModelProviderInput, frozen.NativeOptions, nil
}
