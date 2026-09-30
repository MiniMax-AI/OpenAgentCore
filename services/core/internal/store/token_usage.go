package store

import (
	"context"
	"encoding/json"
	"fmt"
	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"math"
	"strings"
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
	if usage := measuredUsage(kind, raw); usage != nil {
		payload, err := json.Marshal(usage)
		if err != nil {
			return err
		}
		if err = q.PutTurnUsage(ctx, sqlc.PutTurnUsageParams{SessionID: session, ID: turn, TokenUsage: payload}); err != nil {
			return err
		}
	}
	return projectItemSource(ctx, q, session, turn, kind, sequence, raw, created)
}

func measuredUsage(kind string, raw json.RawMessage) *v1.TokenUsage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	if kind == "cancel_receipt" {
		var applied bool
		if json.Unmarshal(object["applied"], &applied) != nil || !applied {
			return nil
		}
		raw = object["outcome"]
		object = nil
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
		kind = "done"
	}
	if strings.HasPrefix(kind, "execution_") {
		raw = object["done"]
		object = nil
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
		kind = "done"
	}
	if kind == "done" {
		raw = object["usage"]
		object = nil
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
	} else if kind != "usage" {
		return nil
	}

	var tokens map[string]*int64
	if json.Unmarshal(object["tokens"], &tokens) != nil {
		return nil
	}
	for _, key := range []string{"input_tokens", "output_tokens", "cached_input_tokens", "reasoning_output_tokens", "total_tokens"} {
		if tokens[key] == nil || *tokens[key] < 0 {
			return nil
		}
	}
	input, output, cached, reasoning, total := *tokens["input_tokens"], *tokens["output_tokens"], *tokens["cached_input_tokens"], *tokens["reasoning_output_tokens"], *tokens["total_tokens"]
	if cached > input || reasoning > output || input > math.MaxInt64-output || total != input+output {
		return nil
	}
	return &v1.TokenUsage{InputTokens: input, OutputTokens: output, TotalTokens: total, InputTokensDetails: v1.InputTokenDetails{CachedTokens: cached}, OutputTokensDetails: v1.OutputTokenDetails{ReasoningTokens: reasoning}}
}
