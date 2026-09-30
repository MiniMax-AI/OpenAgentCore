package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
)

// MeasuredSessionUsage returns Core-internal measured usage for Runtime
// telemetry: the sum of every recorded root Turn snapshot, active Turns
// included, and null only when nothing is recorded. Public Session usage keeps
// the official rule of SessionTokenUsage. A missing Session reads as null, so
// callers resolve the Session first.
func (s *Store) MeasuredSessionUsage(ctx context.Context, tenantID, sessionID string) (json.RawMessage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return nil, err
	}
	usage, err := s.queries.SessionMeasuredTokenUsage(ctx, sqlc.SessionMeasuredTokenUsageParams{TenantID: tenant, ID: id})
	if err != nil {
		return nil, fmt.Errorf("read measured session usage: %w", err)
	}
	return usage, nil
}
