package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
)

// LoadItem reads what the Session holds for update's Item: the stored Item and,
// when update asks for them, whether the Turn has another assistant message and
// the result the application saved for the call.
func (t *SessionTx) LoadItem(ctx context.Context, turnID string, update items.Update) (items.Stored, error) {
	var stored items.Stored
	turn, err := parseID(turnID)
	if err != nil {
		return stored, err
	}
	id, err := pgunit.ParseID(update.Item.ID)
	if err != nil {
		return stored, err
	}
	if update.NeedsNativeMessage() {
		if stored.NativeMessage, err = t.q.HasNativeMessageItem(ctx, sqlc.HasNativeMessageItemParams{TurnID: turn, ID: id}); err != nil {
			return stored, err
		}
	}
	row, err := t.q.GetSessionItem(ctx, sqlc.GetSessionItemParams{SessionID: t.session, ID: id})
	if err == nil {
		err = json.Unmarshal(row.Payload, &stored.Item)
	} else if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return stored, err
	}
	if call, ok := update.ResultCall(); ok {
		stored.FunctionResult, err = t.q.FunctionItemResult(ctx, sqlc.FunctionItemResultParams{SessionID: t.session, TurnID: turn, CallID: call})
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
	}
	return stored, err
}

// PutItem stores a decided Item change. A new Item takes the Session's next
// position and, when it is output, the Turn's next output index; an update
// keeps both, and its first terminal status records when it settled. PutItem
// returns the Item's output index, nil when it has none, and
// textvalue.ErrUnstorable for text PostgreSQL cannot store.
func (t *SessionTx) PutItem(ctx context.Context, turnID string, created time.Time, change items.Change) (*int32, error) {
	turn, err := parseID(turnID)
	if err != nil {
		return nil, err
	}
	id, err := pgunit.ParseID(change.Item.ID)
	if err != nil {
		return nil, err
	}
	payload, err := change.Item.MarshalStored()
	if err != nil {
		return nil, err
	}
	row, err := t.q.PutSessionItem(ctx, sqlc.PutSessionItemParams{ID: id, SessionID: t.session, TurnID: turn, CreatedAt: pgtype.Timestamptz{Time: created, Valid: !created.IsZero()}, Payload: payload, IsOutput: change.Output})
	if err != nil {
		return nil, storable(err)
	}
	return outputIndex(row.OutputIndex), nil
}
