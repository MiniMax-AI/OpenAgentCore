package sessionpg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var _ sessions.ModelExecutionReader = (*Store)(nil)

func (s *Store) SessionModelExecution(ctx context.Context, tenant, session string) (*v1.ModelProviderInput, error) {
	tenantID, err := parseID(tenant)
	if err != nil {
		return nil, err
	}
	sessionID, err := parseID(session)
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.units.Queries().GetSessionModelExecution(ctx, sqlc.GetSessionModelExecutionParams{TenantID: tenantID, SessionID: sessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, sessions.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.cipher == nil {
		return nil, credentialcrypto.ErrUnavailable
	}
	raw, err := s.cipher.OpenModelExecution(ciphertext, tenant, session)
	if err != nil {
		return nil, fmt.Errorf("open session model execution: %w", err)
	}
	var provider v1.ModelProviderInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&provider) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid stored model execution configuration")
	}
	if err := provider.Validate(); err != nil {
		return nil, err
	}
	return &provider, nil
}
