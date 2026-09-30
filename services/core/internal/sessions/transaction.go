package sessions

import (
	"context"
	"encoding/json"
)

// The procedures in this package run inside their caller's Session transaction,
// under the Session lock, and read and write only through the transaction
// interfaces declared beside them. Each interface lists exactly what its
// procedure calls. A procedure never begins, commits or rolls back a
// transaction: when it fails, the caller's transaction rolls back everything the
// procedure wrote.

// LockedSession is what locking a Session row shows.
type LockedSession struct {
	// Deleted reports that the Session was publicly deleted. Its row stays so
	// that remaining execution can settle.
	Deleted bool
}

// Public applies the public view of a locked Session: a deleted Session is
// ErrNotFound.
func (s LockedSession) Public() error {
	if s.Deleted {
		return ErrNotFound
	}
	return nil
}

// JournalTx reads the Session's public usage and journals its public changes.
type JournalTx interface {
	// LoadUsage reads the Session's public usage as the transaction has left it.
	LoadUsage(ctx context.Context) (json.RawMessage, error)
	// AppendChanges journals public changes in order.
	AppendChanges(ctx context.Context, changes ...SessionChange) error
}

// ActiveTurnTx reads the Session's active Turn.
type ActiveTurnTx interface {
	// LoadActiveTurn reads the Session's queued, in-progress or waiting Turn and
	// reports whether it has one.
	LoadActiveTurn(ctx context.Context) (Turn, bool, error)
}
