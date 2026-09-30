package store

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func runtimeUUID(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
