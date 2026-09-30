// Package sessionpg writes the Session changes that the sessions and items
// packages decide to PostgreSQL. Its functions run inside the caller's Session
// transaction, under the Session lock, on the transaction-bound queries: they
// load the facts a decision reads, then apply the decision, allocating event
// sequence positions, event IDs and Item positions, writing the public change
// journal, Items, Turn usage and Artifacts, and pruning the journal. They decide
// nothing.
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
