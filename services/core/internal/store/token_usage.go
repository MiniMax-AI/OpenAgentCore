package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5/pgtype"
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

func projectSource(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID, kind string, sequence int64, raw json.RawMessage, created pgtype.Timestamptz) error {
	if kind == proto.TypeSubagentIdentity {
		return projectSubagentIdentity(ctx, q, session, turn, int32(sequence), raw)
	}
	switch kind {
	case proto.TypeSubagentLifecycle:
		return projectSubagentLifecycle(ctx, q, session, raw)
	case proto.TypeSubagentTurn:
		return projectSubagentTurn(ctx, q, session, raw)
	case proto.TypeSubagentItem:
		return projectSubagentItem(ctx, q, session, raw)
	case proto.TypeSubagentCoordination:
		return projectRootCoordination(ctx, q, session, turn, raw, created)
	}
	if usage := sessions.MeasuredUsage(kind, raw); usage != nil {
		if err := sessionpg.PutTurnUsage(ctx, q, session, turn, *usage); err != nil {
			return err
		}
	}
	return projectItemSource(ctx, q, session, turn, kind, sequence, raw, created)
}
