package sessions

import "context"

// JournalExecution records execution observations in Turn journals.
type JournalExecution interface {
	// WithTurnJournal runs apply in a Session transaction on the lease-bound
	// connection, with the tenant's Session locked, and passes apply the
	// transaction's context. A deleted Session still records its Turns'
	// observations.
	WithTurnJournal(ctx context.Context, tenant, session string, apply func(context.Context, TurnJournalTx) error) error
}

// AppendTurnEvents records an ordered batch of a Turn's execution observations
// in its journal from position first and projects them, as the
// AppendTurnEvents procedure decides. A malformed tenant, Session or Turn ID is
// ErrInvalidInput, before the batch is validated and before the Session is
// looked up. The journal is Core-internal; it is not the public event stream.
func (o *ExecutionOperations) AppendTurnEvents(ctx context.Context, tenant, session, turn string, first int32, events []ExecutionEvent) error {
	if !validID(tenant) || !validID(session) {
		return ErrInvalidInput
	}
	batch, err := NewJournalBatch(turn, first, events)
	if err != nil {
		return err
	}
	return o.storage.WithTurnJournal(ctx, tenant, session, func(ctx context.Context, tx TurnJournalTx) error {
		return AppendTurnEvents(ctx, tx, batch)
	})
}
