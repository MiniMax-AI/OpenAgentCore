package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ArchivedCancellationReceipt is a read-only exception for the exact already
// authenticated delivery. The ordinary credential view remains revoked; this
// cannot authorize bootstrap, reconnect, dispatch, workspace access or renewal.
// A marker records that archive caused the first revocation; timestamps alone
// cannot distinguish an earlier ordinary cancel/revoke followed by archive.
func (s *Store) ArchivedCancellationReceipt(ctx context.Context, deviceID, credentialHash string, runIDs []string) (device.ArchivedCancellationReceipt, error) {
	if len(runIDs) == 0 || credentialHash == "" {
		return device.ArchivedCancellationReceipt{}, nil
	}
	id, err := parseID(deviceID)
	if err != nil {
		return device.ArchivedCancellationReceipt{}, err
	}
	row, err := s.queries.GetArchivedCancellationReceipt(ctx, sqlc.GetArchivedCancellationReceiptParams{DeviceID: id, CredentialHash: pgtype.Text{String: credentialHash, Valid: true}, RunIds: runIDs, LimitSeconds: int32(device.ArchivedCancellationReceiptLimit.Seconds())})
	if errors.Is(err, pgx.ErrNoRows) {
		return device.ArchivedCancellationReceipt{}, nil
	}
	if err != nil {
		return device.ArchivedCancellationReceipt{}, err
	}
	return device.ArchivedCancellationReceipt{RunID: uuid.UUID(row.ID.Bytes).String(), Deadline: row.CancelRequestedAt.Time.Add(device.ArchivedCancellationReceiptLimit)}, nil
}
