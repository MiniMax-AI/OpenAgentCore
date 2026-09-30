package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// UnstorableText reports PostgreSQL rejecting client text it cannot represent:
// U+0000 or invalid UTF-8 in a text parameter (22021), or a jsonb \u0000 escape
// (22P05). The rejected statement stores nothing, and writes sharing its
// transaction roll back.
func UnstorableText(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && (databaseError.Code == "22021" || databaseError.Code == "22P05")
}
