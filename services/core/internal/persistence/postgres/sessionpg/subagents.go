package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// loadPublicSubagent loads a Subagent of the Session as the public API shows
// it. A missing or malformed ID is sessions.ErrNotFound.
func loadPublicSubagent(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, id string) (v1.Subagent, error) {
	row, err := q.GetPublicSubagent(ctx, sqlc.GetPublicSubagentParams{SessionID: session, ID: pgunit.PathID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return v1.Subagent{}, sessions.ErrNotFound
	}
	if err != nil {
		return v1.Subagent{}, err
	}
	result := v1.Subagent{ID: id, SessionID: uuid.UUID(session.Bytes).String(), ParentAgentID: row.ParentAgentID, Object: "agent.session.subagent", Status: row.Status, OpenedAt: row.NativeCreatedAt}
	if row.Name.Valid {
		result.Name = &row.Name.String
	}
	if row.Instructions.Valid {
		result.Instructions = []v1.AgentContent{{Type: "output_text", Text: &row.Instructions.String}}
	}
	if row.ClosedAtMs.Valid {
		seconds := row.ClosedAtMs.Int64 / 1000
		result.ClosedAt = &seconds
	}
	return result, nil
}

// loadChildTurn loads a Turn of the Session's Subagent child. A missing or
// malformed ID, a root Turn and another child's Turn are sessions.ErrNotFound.
func loadChildTurn(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, child, id string) (sqlc.SubagentTurn, error) {
	row, err := q.GetChildTurn(ctx, sqlc.GetChildTurnParams{SessionID: session, ID: pgunit.PathID(id)})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && uuid.UUID(row.SubagentID.Bytes).String() != child) {
		return row, sessions.ErrNotFound
	}
	return row, err
}

func (t *SessionTx) LoadRootAgent(ctx context.Context) (string, error) {
	return t.q.SubagentRootAgent(ctx, t.session)
}

func (t *SessionTx) LoadNativeSubagent(ctx context.Context, native string) (sessions.NativeSubagent, bool, error) {
	row, err := t.q.GetNativeSubagent(ctx, sqlc.GetNativeSubagentParams{SessionID: t.session, NativeID: native})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.NativeSubagent{}, false, nil
	}
	if err != nil {
		return sessions.NativeSubagent{}, false, err
	}
	return sessions.NativeSubagent{
		ID: uuid.UUID(row.ID.Bytes).String(), Visible: row.PublicVisible, NativeCreatedAt: row.NativeCreatedAt,
		Status: row.Status, LifecycleAtMS: row.LifecycleAtMs,
	}, true, nil
}

// PutSubagentIdentity allocates the Subagent ID of a new binding.
func (t *SessionTx) PutSubagentIdentity(ctx context.Context, identity sessions.SubagentIdentity) (string, error) {
	turn, err := parseID(identity.FirstTurn)
	if err != nil {
		return "", err
	}
	id, err := t.q.PutSubagentIdentity(ctx, sqlc.PutSubagentIdentityParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: t.session,
		NativeID: identity.NativeID, ParentNativeID: identity.ParentNativeID, NativeCreatedAt: identity.NativeCreatedAt,
		FirstTurnID: turn, FirstEventOrdinal: identity.FirstOrdinal,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", sessions.ErrIdempotencyConflict
	}
	if err != nil {
		return "", err
	}
	return uuid.UUID(id.Bytes).String(), nil
}

func (t *SessionTx) PublishSubagent(ctx context.Context, subagent string, name, instructions *string) error {
	id, err := parseID(subagent)
	if err != nil {
		return err
	}
	p := sqlc.PublishSubagentParams{SessionID: t.session, ID: id}
	if name != nil {
		p.Name = pgtype.Text{String: *name, Valid: true}
	}
	if instructions != nil {
		p.Instructions = pgtype.Text{String: *instructions, Valid: true}
	}
	return t.q.PublishSubagent(ctx, p)
}

func (t *SessionTx) LoadPublicSubagent(ctx context.Context, id string) (v1.Subagent, error) {
	return loadPublicSubagent(ctx, t.q, t.session, id)
}

func (t *SessionTx) PutSubagentEffect(ctx context.Context, effect string, payload json.RawMessage) (bool, error) {
	inserted, err := t.q.PutSubagentEffect(ctx, sqlc.PutSubagentEffectParams{SessionID: t.session, EffectID: effect, Payload: payload})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, sessions.ErrIdempotencyConflict
	}
	return inserted, err
}

func (t *SessionTx) ApplySubagentLifecycle(ctx context.Context, subagent string, lifecycle sessions.SubagentLifecycle) error {
	id, err := parseID(subagent)
	if err != nil {
		return err
	}
	closed := pgtype.Int8{}
	if lifecycle.ClosedAtMS != nil {
		closed = pgtype.Int8{Int64: *lifecycle.ClosedAtMS, Valid: true}
	}
	return t.q.ApplySubagentLifecycle(ctx, sqlc.ApplySubagentLifecycleParams{SessionID: t.session, ID: id, Status: lifecycle.Status, ClosedAtMs: closed, LifecycleAtMs: lifecycle.AtMS})
}

func (t *SessionTx) LoadChildTurn(ctx context.Context, subagent, id string) (sessions.ChildTurn, bool, error) {
	row, err := loadChildTurn(ctx, t.q, t.session, subagent, id)
	if errors.Is(err, sessions.ErrNotFound) {
		return sessions.ChildTurn{}, false, nil
	}
	if err != nil {
		return sessions.ChildTurn{}, false, err
	}
	return sessions.ChildTurn{
		ID: uuid.UUID(row.ID.Bytes).String(), Subagent: uuid.UUID(row.SubagentID.Bytes).String(), NativeID: row.NativeID, Status: row.Status,
		CreatedAtMS: row.CreatedAt.Time.UnixMilli(), StartedAtMS: nativeMillis(row.StartedAt), CompletedAtMS: nativeMillis(row.CompletedAt),
		Usage: json.RawMessage(row.TokenUsage),
	}, true, nil
}

func (t *SessionTx) PutChildTurn(ctx context.Context, turn sessions.ChildTurn) error {
	id, err := parseID(turn.ID)
	if err != nil {
		return err
	}
	subagent, err := parseID(turn.Subagent)
	if err != nil {
		return err
	}
	_, err = t.q.PutChildTurn(ctx, sqlc.PutChildTurnParams{
		ID: id, SessionID: t.session, SubagentID: subagent, NativeID: turn.NativeID, Status: turn.Status,
		CreatedAt: pgtype.Timestamptz{Time: time.UnixMilli(turn.CreatedAtMS), Valid: true}, StartedAt: nativeTime(turn.StartedAtMS), CompletedAt: nativeTime(turn.CompletedAtMS),
		TokenUsage: turn.Usage,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ErrIdempotencyConflict
	}
	return err
}

func (t *SessionTx) LoadChildItem(ctx context.Context, subagent, item string, candidate json.RawMessage) (sessions.StoredChildItem, bool, error) {
	child, err := parseID(subagent)
	if err != nil {
		return sessions.StoredChildItem{}, false, err
	}
	id, err := parseID(item)
	if err != nil {
		return sessions.StoredChildItem{}, false, err
	}
	row, err := t.q.GetChildItem(ctx, sqlc.GetChildItemParams{SessionID: t.session, SubagentID: child, ID: id, Candidate: candidate})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.StoredChildItem{}, false, nil
	}
	if err != nil {
		return sessions.StoredChildItem{}, false, err
	}
	return sessions.StoredChildItem{Turn: uuid.UUID(row.TurnID.Bytes).String(), Position: row.Position, Payload: row.Payload, Same: row.PayloadEqual}, true, nil
}

// PutChildItem stores a Subagent Item; a new output Item takes its Turn's next
// output index.
func (t *SessionTx) PutChildItem(ctx context.Context, item sessions.ChildItem) error {
	id, err := parseID(item.ID)
	if err != nil {
		return err
	}
	subagent, err := parseID(item.Subagent)
	if err != nil {
		return err
	}
	turn, err := parseID(item.Turn)
	if err != nil {
		return err
	}
	_, err = t.q.PutChildItem(ctx, sqlc.PutChildItemParams{ID: id, SessionID: t.session, SubagentID: subagent, TurnID: turn, Position: item.Position, Payload: item.Payload, IsOutput: item.Output})
	return err
}

// nativeTime stores native milliseconds; nil is absent.
func nativeTime(value *int64) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: time.UnixMilli(*value), Valid: true}
}

// nativeMillis reads stored native milliseconds; absent is nil.
func nativeMillis(value pgtype.Timestamptz) *int64 {
	if !value.Valid {
		return nil
	}
	ms := value.Time.UnixMilli()
	return &ms
}
