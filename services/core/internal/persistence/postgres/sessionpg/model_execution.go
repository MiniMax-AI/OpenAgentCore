package sessionpg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
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
	if err != nil {
		return nil, errors.New("session model execution configuration is unavailable")
	}
	raw, err := s.cipher.OpenModelExecution(ciphertext, tenant, session)
	if err != nil {
		return nil, errors.New("session model execution decryption is unavailable")
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
