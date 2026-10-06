package sessions

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
)

// A Turn's journal holds its execution observations in order. One batch holds
// at most 64 observations of at most 512 KiB each and 1 MiB together, and one
// Turn's journal at most 65,536 observations and 32 MiB. The terminal outcome
// is recorded beyond those limits, in the one entry they reserve for it.
const (
	journalBatchEvents  = 64
	journalPayloadBytes = 512 * 1024
	journalBatchBytes   = 1024 * 1024
	journalTurnEvents   = 65536
	journalTurnBytes    = 32 * 1024 * 1024
)

// JournalBatch is a validated batch of execution observations for a Turn's
// journal, each payload normalized. NewJournalBatch makes one.
type JournalBatch struct {
	turn   string
	first  int32
	events []ExecutionEvent
	bytes  int64
}

// NewJournalBatch validates an ordered batch of a Turn's execution
// observations that starts at journal position first. A malformed Turn ID is
// ErrInvalidInput before the batch is checked. Each observation needs a kind
// and a JSON object payload within the journal limits; a batch over the batch
// size limit is ErrEventLimit, and anything else invalid is ErrInvalidInput.
func NewJournalBatch(turn string, first int32, events []ExecutionEvent) (JournalBatch, error) {
	if !validID(turn) || first < 1 || len(events) == 0 || len(events) > journalBatchEvents {
		return JournalBatch{}, ErrInvalidInput
	}
	batch := JournalBatch{turn: turn, first: first, events: make([]ExecutionEvent, len(events))}
	for i, event := range events {
		if len(event.Payload) > journalPayloadBytes || !ValidEngine(event.Kind) {
			return JournalBatch{}, ErrInvalidInput
		}
		payload, err := jsonobject.Normalize(event.Payload)
		if err != nil {
			return JournalBatch{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
		batch.events[i] = ExecutionEvent{Kind: event.Kind, Payload: payload}
		batch.bytes += int64(len(payload))
	}
	if batch.bytes > journalBatchBytes {
		return JournalBatch{}, ErrEventLimit
	}
	return batch, nil
}

// validID reports whether value can name a stored resource: a nonzero UUID.
func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

// JournalTurn is what decides whether a Turn's journal records a new batch:
// the Turn's status and what its journal holds.
type JournalTurn struct {
	Status     string
	EventCount int32
	EventBytes int64
}

// admit decides whether turn records b as its next journal entries: only an
// in-progress or waiting Turn records a batch, only at the position after its
// last entry (ErrTurnConflict), and only within the journal limits
// (ErrEventLimit).
func (b JournalBatch) admit(turn JournalTurn) error {
	if (turn.Status != TurnInProgress && turn.Status != TurnWaiting) || b.first != turn.EventCount+1 {
		return ErrTurnConflict
	}
	if turn.EventCount+int32(len(b.events)) > journalTurnEvents || turn.EventBytes+b.bytes > journalTurnBytes {
		return ErrEventLimit
	}
	return nil
}

// EventProjectionTx counts a Turn's new journal entries and projects them.
type EventProjectionTx interface {
	ProjectionTx
	// CountEvents adds count entries of size payload bytes to what the Turn's
	// journal holds.
	CountEvents(ctx context.Context, turn string, count int32, size int64) error
	// LoadEventSources reads the Turn's journal entries from position first, in
	// order.
	LoadEventSources(ctx context.Context, turn string, first int32) ([]Source, error)
}

// TurnJournalTx records a batch in a Turn's journal and projects it.
type TurnJournalTx interface {
	EventProjectionTx
	// LoadJournalTurn reads the tenant's Turn in the Session and reports whether
	// it exists.
	LoadJournalTurn(ctx context.Context, turn string) (JournalTurn, bool, error)
	// MatchEvents reports whether the Turn's journal holds exactly events from
	// position first.
	MatchEvents(ctx context.Context, turn string, first int32, events []ExecutionEvent) (bool, error)
	// InsertEvents records events at the Turn's journal positions from first.
	InsertEvents(ctx context.Context, turn string, first int32, events []ExecutionEvent) error
}

// TurnEventTx records one entry in a Turn's journal and projects it.
type TurnEventTx interface {
	EventProjectionTx
	// InsertEvent records event at the Turn's journal position ordinal.
	InsertEvent(ctx context.Context, turn string, ordinal int32, event ExecutionEvent) error
}

// AppendTurnEvents records batch in its Turn's journal and projects the new
// entries. A batch the journal already holds from the same position is an
// idempotent replay and changes nothing; a different batch there is
// ErrIdempotencyConflict. A new batch is recorded only as JournalBatch admits
// it, and a missing Turn is ErrNotFound.
func AppendTurnEvents(ctx context.Context, tx TurnJournalTx, batch JournalBatch) error {
	if len(batch.events) == 0 {
		return ErrInvalidInput
	}
	turn, found, err := tx.LoadJournalTurn(ctx, batch.turn)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if batch.first <= turn.EventCount {
		matches, err := tx.MatchEvents(ctx, batch.turn, batch.first, batch.events)
		if err != nil {
			return err
		}
		if !matches {
			return ErrIdempotencyConflict
		}
		return nil
	}
	if err := batch.admit(turn); err != nil {
		return err
	}
	if err := tx.InsertEvents(ctx, batch.turn, batch.first, batch.events); err != nil {
		return err
	}
	if err := projectEvents(ctx, tx, batch.turn, batch.first); err != nil {
		return err
	}
	return tx.CountEvents(ctx, batch.turn, int32(len(batch.events)), batch.bytes)
}

// AppendTurnEvent records event in the Turn's journal after its last entry,
// last, which the caller read under the Session lock, and projects it. It
// records the terminal outcome, which the journal limits reserve an entry for.
func AppendTurnEvent(ctx context.Context, tx TurnEventTx, turn string, last int32, event ExecutionEvent) error {
	if err := tx.InsertEvent(ctx, turn, last+1, event); err != nil {
		return err
	}
	if err := tx.CountEvents(ctx, turn, 1, int64(len(event.Payload))); err != nil {
		return err
	}
	return projectEvents(ctx, tx, turn, last+1)
}

// projectEvents projects the Turn's journal entries from position first, in
// order.
func projectEvents(ctx context.Context, tx EventProjectionTx, turn string, first int32) error {
	sources, err := tx.LoadEventSources(ctx, turn, first)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if err := ProjectSource(ctx, tx, source); err != nil {
			return err
		}
	}
	return nil
}
