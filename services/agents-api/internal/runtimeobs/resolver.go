package runtimeobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

var (
	ErrUnavailable = errors.New("Runtime observation unavailable")
	ErrNotRunning  = errors.New("Runtime is not running")
)

type sessionStore interface {
	GetSession(context.Context, string, string) (store.Session, error)
	GetRuntimeAllocation(context.Context, string, string) (store.RuntimeAllocation, error)
}

type Resolver struct{ store sessionStore }

func NewResolver(s sessionStore) (*Resolver, error) {
	if s == nil {
		return nil, errors.New("Runtime observation store is required")
	}
	return &Resolver{store: s}, nil
}

func (r *Resolver) Resolve(ctx context.Context, tenantID, sessionID string) (Target, error) {
	session, err := r.store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return Target{}, fmt.Errorf("resolve Runtime Session: %w", err)
	}
	var configuration struct {
		Environment *struct {
			Type string `json:"type"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(session.Configuration, &configuration); err != nil || configuration.Environment == nil {
		return Target{}, errors.New("invalid stored Runtime environment configuration")
	}
	target := Target{TenantID: session.TenantID, SessionID: session.ID, Mode: Mode(configuration.Environment.Type)}
	switch target.Mode {
	case ModeNone:
		if session.Environment != nil {
			return Target{}, errors.New("environment:none unexpectedly has a durable Environment")
		}
		return target, nil
	case ModeSelfHosted:
		if session.Environment == nil {
			return Target{}, errors.New("self-hosted Session is missing its Environment")
		}
		if session.Environment.TenantID != session.TenantID || session.Environment.SessionID != session.ID {
			return Target{}, errors.New("self-hosted Environment does not match resolved ownership")
		}
		target.EnvironmentID = session.Environment.ID
		return target, nil
	case ModeManaged:
		if session.Environment == nil {
			return Target{}, errors.New("managed Session is missing its Environment")
		}
		if session.Environment.TenantID != session.TenantID || session.Environment.SessionID != session.ID {
			return Target{}, errors.New("managed Environment does not match resolved ownership")
		}
		target.EnvironmentID = session.Environment.ID
		allocation, err := r.store.GetRuntimeAllocation(ctx, tenantID, target.EnvironmentID)
		if errors.Is(err, store.ErrNotFound) {
			return target, ErrUnavailable
		}
		if err != nil {
			return Target{}, fmt.Errorf("resolve Runtime allocation: %w", err)
		}
		if allocation.TenantID != tenantID || allocation.SessionID != session.ID || allocation.EnvironmentID != target.EnvironmentID {
			return Target{}, errors.New("Runtime allocation does not match resolved ownership")
		}
		target.Instance = Instance{
			AllocationID: allocation.ID, ProviderKey: allocation.ProviderKey, DeviceID: allocation.DeviceID,
			AllocationState: allocation.State, AllocationCreatedAt: allocation.CreatedAt,
			ComputePhase: allocation.ComputePhase, ProviderState: append(json.RawMessage(nil), allocation.ComputeState...),
		}
		return target, nil
	default:
		return Target{}, errors.New("invalid stored Runtime environment type")
	}
}
