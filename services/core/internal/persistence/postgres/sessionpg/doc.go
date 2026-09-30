// Package sessionpg stores Sessions in PostgreSQL and decides nothing.
// WithSession runs a Session transaction: it locks the Session, runs the
// operation and prunes the journal. Inside a Session transaction, on its
// queries, the participants load the facts the sessions and items decisions
// read and apply what they decide: they lock the Session, allocate event
// sequence positions, event IDs, Item positions and output indexes, and write
// the public change journal, Items, Turn usage, Artifact settlement,
// Environment state, input reservations and devices. SessionTx binds a Session
// to its caller's transaction for the sessions procedures.
package sessionpg

import (
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
)

// storable returns textvalue.ErrUnstorable in place of PostgreSQL rejecting
// client text it cannot represent, and any other error unchanged.
func storable(err error) error {
	if pgunit.IsUnstorableText(err) {
		return textvalue.ErrUnstorable
	}
	return err
}

func outputIndex(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	index := value.Int32
	return &index
}
