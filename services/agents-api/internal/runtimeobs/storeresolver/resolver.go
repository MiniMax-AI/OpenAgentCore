package storeresolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type sessionStore interface {
	GetSession(context.Context, string, string) (store.Session, error)
	MeasuredSessionUsage(context.Context, string, string) (json.RawMessage, error)
	GetRuntimeAllocation(context.Context, string, string) (store.RuntimeAllocation, error)
	ListRuntimeObservationSessions(context.Context, string, int) (store.RuntimeObservationSessionPage, error)
}

type Resolver struct{ store sessionStore }

func NewResolver(s sessionStore) (*Resolver, error) {
	if s == nil {
		return nil, errors.New("Runtime observation store is required")
	}
	return &Resolver{store: s}, nil
}

func (r *Resolver) Resolve(ctx context.Context, tenantID, sessionID string) (runtimeobs.Target, error) {
	session, err := r.store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return runtimeobs.Target{}, fmt.Errorf("resolve Runtime Session: %w", err)
	}
	var configuration struct {
		Environment *struct {
			Type string `json:"type"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(session.Configuration, &configuration); err != nil || configuration.Environment == nil {
		return runtimeobs.Target{}, errors.New("invalid stored Runtime environment configuration")
	}
	target := runtimeobs.Target{TenantID: session.TenantID, SessionID: session.ID, Mode: runtimeobs.Mode(configuration.Environment.Type)}
	// Telemetry counts measured usage continuously, including active Turns.
	// Public Session usage stays null until every root Turn ends measured.
	measured, err := r.store.MeasuredSessionUsage(ctx, session.TenantID, session.ID)
	if err != nil {
		return runtimeobs.Target{}, fmt.Errorf("resolve Runtime Session usage: %w", err)
	}
	usage, err := decodeTokenUsage(measured)
	if err != nil {
		return runtimeobs.Target{}, err
	}
	target.TokenUsage = usage
	switch target.Mode {
	case runtimeobs.ModeNone:
		if session.Environment != nil {
			return runtimeobs.Target{}, errors.New("environment:none unexpectedly has a durable Environment")
		}
		return target, nil
	case runtimeobs.ModeSelfHosted:
		if session.Environment == nil {
			return runtimeobs.Target{}, errors.New("self-hosted Session is missing its Environment")
		}
		if session.Environment.TenantID != session.TenantID || session.Environment.SessionID != session.ID {
			return runtimeobs.Target{}, errors.New("self-hosted Environment does not match resolved ownership")
		}
		target.EnvironmentID = session.Environment.ID
		return target, nil
	case runtimeobs.ModeManaged:
		if session.Environment == nil {
			return runtimeobs.Target{}, errors.New("managed Session is missing its Environment")
		}
		if session.Environment.TenantID != session.TenantID || session.Environment.SessionID != session.ID {
			return runtimeobs.Target{}, errors.New("managed Environment does not match resolved ownership")
		}
		target.EnvironmentID = session.Environment.ID
		allocation, err := r.store.GetRuntimeAllocation(ctx, tenantID, target.EnvironmentID)
		if errors.Is(err, store.ErrNotFound) {
			return target, runtimeobs.ErrUnavailable
		}
		if err != nil {
			return runtimeobs.Target{}, fmt.Errorf("resolve Runtime allocation: %w", err)
		}
		if allocation.TenantID != tenantID || allocation.SessionID != session.ID || allocation.EnvironmentID != target.EnvironmentID {
			return runtimeobs.Target{}, errors.New("Runtime allocation does not match resolved ownership")
		}
		target.Instance = runtimeobs.Instance{
			AllocationID: allocation.ID, ProviderKey: allocation.ProviderKey, DeviceID: allocation.DeviceID,
			AllocationState: allocation.State, AllocationCreatedAt: allocation.CreatedAt,
			ComputePhase: allocation.ComputePhase, ProviderState: append(json.RawMessage(nil), allocation.ComputeState...),
		}
		return target, nil
	default:
		return runtimeobs.Target{}, errors.New("invalid stored Runtime environment type")
	}
}

type storedTokenUsage struct {
	InputTokens         *int64                    `json:"input_tokens"`
	InputTokensDetails  *storedInputTokenDetails  `json:"input_tokens_details"`
	OutputTokens        *int64                    `json:"output_tokens"`
	OutputTokensDetails *storedOutputTokenDetails `json:"output_tokens_details"`
	TotalTokens         *int64                    `json:"total_tokens"`
}

type storedInputTokenDetails struct {
	CachedTokens *int64 `json:"cached_tokens"`
}

type storedOutputTokenDetails struct {
	ReasoningTokens *int64 `json:"reasoning_tokens"`
}

func decodeTokenUsage(raw json.RawMessage) (*runtimeobs.TokenUsage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var usage storedTokenUsage
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&usage); err != nil {
		return nil, errors.New("invalid stored Session token usage")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid stored Session token usage")
	}
	if usage.InputTokens == nil || usage.OutputTokens == nil || usage.TotalTokens == nil ||
		usage.InputTokensDetails == nil || usage.InputTokensDetails.CachedTokens == nil ||
		usage.OutputTokensDetails == nil || usage.OutputTokensDetails.ReasoningTokens == nil {
		return nil, errors.New("invalid stored Session token usage")
	}
	const maxSafeInteger = int64(1<<53 - 1)
	values := []int64{*usage.InputTokens, *usage.OutputTokens, *usage.TotalTokens, *usage.InputTokensDetails.CachedTokens, *usage.OutputTokensDetails.ReasoningTokens}
	for _, value := range values {
		if value < 0 || value > maxSafeInteger {
			return nil, errors.New("invalid stored Session token usage")
		}
	}
	if *usage.InputTokens+*usage.OutputTokens != *usage.TotalTokens {
		return nil, errors.New("invalid stored Session token usage")
	}
	return &runtimeobs.TokenUsage{InputTokens: uint64(*usage.InputTokens), OutputTokens: uint64(*usage.OutputTokens)}, nil
}

func (r *Resolver) ListRuntimeObservationSessions(ctx context.Context, after string, limit int) (runtimeobs.SessionPage, error) {
	page, err := r.store.ListRuntimeObservationSessions(ctx, after, limit)
	if err != nil {
		return runtimeobs.SessionPage{}, err
	}
	result := runtimeobs.SessionPage{Sessions: make([]runtimeobs.SessionIdentity, 0, len(page.Sessions)), NextCursor: page.NextCursor}
	for _, session := range page.Sessions {
		result.Sessions = append(result.Sessions, runtimeobs.SessionIdentity{TenantID: session.TenantID, SessionID: session.SessionID})
	}
	return result, nil
}
