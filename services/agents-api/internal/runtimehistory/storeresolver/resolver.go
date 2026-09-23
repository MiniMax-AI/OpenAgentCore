package storeresolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type environmentStore interface {
	GetSessionEnvironment(context.Context, string, string) (store.Environment, error)
}

type Resolver struct{ store environmentStore }

func NewResolver(value environmentStore) (*Resolver, error) {
	if value == nil {
		return nil, errors.New("Runtime history store is required")
	}
	return &Resolver{store: value}, nil
}

// ResolveRuntimeHistoryScope authorizes the Session through the Core store and
// returns only durable Core identity. It intentionally does not resolve a
// current allocation: retained history may contain earlier allocations. The
// Reader keeps each durable allocation as one continuous series.
func (r *Resolver) ResolveRuntimeHistoryScope(ctx context.Context, tenantID, sessionID string) (runtimehistory.Scope, error) {
	environment, err := r.store.GetSessionEnvironment(ctx, tenantID, sessionID)
	if err != nil {
		return runtimehistory.Scope{}, fmt.Errorf("resolve Runtime history Environment: %w", err)
	}
	if environment.TenantID != tenantID || environment.SessionID != sessionID {
		return runtimehistory.Scope{}, errors.New("Runtime history Environment does not match resolved ownership")
	}
	var configuration struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(environment.Configuration, &configuration) != nil || configuration.Type == "" {
		return runtimehistory.Scope{}, errors.New("invalid stored Runtime history environment configuration")
	}
	if configuration.Type != "openai_hosted" {
		return runtimehistory.Scope{}, runtimehistory.ErrUnsupported
	}
	return runtimehistory.Scope{TenantID: tenantID, SessionID: sessionID, EnvironmentID: environment.ID}, nil
}
