package execution

import (
	"context"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (d *Dispatcher) sessionModelProvider(ctx context.Context, session sessions.Session) (*modelprovider.Provider, error) {
	provider, err := d.SessionsReader.SessionModelExecution(ctx, session.TenantID, session.ID)
	if err != nil {
		return nil, err
	}
	return resolvedSessionModelProvider(provider, session.Engine)
}

func resolvedSessionModelProvider(provider *v1.ModelProviderInput, engine string) (*modelprovider.Provider, error) {
	if err := provider.ValidateHarness(engine); err != nil {
		return nil, err
	}
	resolved := provider.Provider()
	return &resolved, nil
}
