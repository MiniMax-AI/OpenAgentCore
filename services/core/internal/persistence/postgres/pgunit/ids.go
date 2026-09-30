package pgunit

import (
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrInvalidID reports a value that is not a nonzero UUID. Adapters translate
// it into their domain's invalid-input error.
var ErrInvalidID = errors.New("nonzero UUID required")

// unknownID never names a stored resource: Core assigns version 4 or 5 UUIDs,
// and the maximum UUID is neither.
var unknownID = uuid.Max

// ParseID parses a resource identifier that must name a resource, such as a
// request-body reference. A value that is not a nonzero UUID is ErrInvalidID.
func ParseID(value string) (pgtype.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return pgtype.UUID{}, ErrInvalidID
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

// PathID parses a caller-supplied resource path identifier. A value that cannot
// name a resource resolves to an identifier that never exists, so the request
// follows exactly the path of a well-formed missing identifier, including
// validation order. Request-body references use ParseID; cursors use
// LookupCursor.
func PathID(value string) pgtype.UUID {
	id, err := ParseID(value)
	if err != nil {
		return pgtype.UUID{Bytes: unknownID, Valid: true}
	}
	return id
}

// LookupCursor resolves the cursor of a list whose unresolved cursor is a 404
// (Agents, Sessions, Turns, Templates, Vaults and Credentials) like a path
// identifier: a value that cannot name a resource becomes an identifier that
// never exists, so the list follows exactly the missing-cursor path.
func LookupCursor(value string) string {
	if _, err := ParseID(value); err != nil {
		return unknownID.String()
	}
	return value
}
