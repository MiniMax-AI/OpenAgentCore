package store

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5/pgtype"
)

func parseConnectionGeneration(value string) (pgtype.UUID, error) {
	id, err := parseID(value)
	if err != nil || id.Bytes == [16]byte{} {
		return pgtype.UUID{}, sessions.ErrInvalidInput
	}
	return id, nil
}
