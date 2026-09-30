package sessionpg

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// LoadEnding reads the facts sessions.EndTurn settles a Turn that ended with:
// its Items still in progress and the Session's public usage.
func LoadEnding(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID) (sessions.Ending, error) {
	rows, err := q.ListUnfinishedSessionItems(ctx, sqlc.ListUnfinishedSessionItemsParams{SessionID: session, TurnID: turn})
	if err != nil {
		return sessions.Ending{}, err
	}
	ending := sessions.Ending{Unfinished: make([]sessions.UnfinishedItem, len(rows))}
	for i, row := range rows {
		var item v1.Item
		if err := json.Unmarshal(row.Payload, &item); err != nil {
			return sessions.Ending{}, err
		}
		ending.Unfinished[i] = sessions.UnfinishedItem{Position: row.Position, OutputIndex: outputIndex(row.OutputIndex), Item: item}
	}
	ending.Usage, err = LoadUsage(ctx, q, session)
	return ending, err
}

// ApplyTurnEnd writes what a Turn that ended settles, in the order
// sessions.TurnEnd lists: terminal activity, Artifacts, finished Items and then
// the public changes.
func ApplyTurnEnd(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID, end sessions.TurnEnd) error {
	if end.TerminalActivity {
		if err := RecordTerminalActivity(ctx, q, session); err != nil {
			return err
		}
	}
	switch end.Artifacts {
	case sessions.PublishArtifacts:
		// The Session lock orders Artifact deletion and allows one active Turn,
		// so the comparison sees exactly the Artifacts that remain when the Turn
		// ends.
		if err := q.DeleteUnchangedTurnArtifacts(ctx, sqlc.DeleteUnchangedTurnArtifactsParams{SessionID: session, TurnID: turn}); err != nil {
			return err
		}
		if err := q.PublishTurnArtifacts(ctx, sqlc.PublishTurnArtifactsParams{SessionID: session, TurnID: turn, CreatedAt: pgtype.Timestamptz{Time: end.PublishedAt, Valid: !end.PublishedAt.IsZero()}}); err != nil {
			return err
		}
	case sessions.DiscardArtifacts:
		if err := q.DeleteUnpublishedTurnArtifacts(ctx, sqlc.DeleteUnpublishedTurnArtifactsParams{SessionID: session, TurnID: turn}); err != nil {
			return err
		}
	}
	if len(end.FinishedItems) > 0 {
		ids := make([]pgtype.UUID, len(end.FinishedItems))
		for i, value := range end.FinishedItems {
			id, err := pgunit.ParseID(value)
			if err != nil {
				return err
			}
			ids[i] = id
		}
		if err := q.FinishSessionItems(ctx, sqlc.FinishSessionItemsParams{Status: end.FinishedStatus, SessionID: session, Ids: ids}); err != nil {
			return err
		}
	}
	return AppendChanges(ctx, q, session, end.Changes...)
}

// RecordTerminalActivity marks the Session's running managed compute active at
// the database's commit clock.
func RecordTerminalActivity(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	return q.RecordRuntimeTerminalActivity(ctx, session)
}
