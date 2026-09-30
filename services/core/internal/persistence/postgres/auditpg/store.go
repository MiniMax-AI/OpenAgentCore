package auditpg

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// Store reads the audit records and applies write audit retention.
type Store struct{ pool *pgunit.Pool }

func New(pool *pgunit.Pool) *Store { return &Store{pool: pool} }

var (
	_ writeaudit.Reader = (*Store)(nil)
	_ adminaudit.Reader = (*Store)(nil)
)

// A page cursor names the last row of its page and is bound to the digest of
// the query that produced it, so it cannot resume another tenant's or filter's
// list.
type cursor struct{ ID, Scope string }

// cursorScope digests a query without its cursor and page size.
func cursorScope(query any) string {
	encoded, _ := json.Marshal(query)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func encodeCursor(id, scope string) string {
	encoded, _ := json.Marshal(cursor{ID: id, Scope: scope})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// decodeCursor returns the row a cursor names, or false when the cursor is
// malformed or belongs to another query.
func decodeCursor(value, scope string) (pgtype.UUID, bool) {
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	var decoded cursor
	if len(value) > 1024 || err != nil || json.Unmarshal(encoded, &decoded) != nil || decoded.Scope != scope {
		return pgtype.UUID{}, false
	}
	id, err := pgunit.ParseID(decoded.ID)
	return id, err == nil
}

// utc normalizes a bound so that equal instants digest to the same scope.
func utc(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

func timestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}
