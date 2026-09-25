package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ConnectRuntimeNode(ctx context.Context, nodeID, connectionID string, epoch uint64) error {
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return err
	}
	connection, err := parseConnectionGeneration(connectionID)
	if err != nil {
		return err
	}
	// A canceled autocommit UPDATE may still finish on PostgreSQL after pgx
	// returns. An explicit transaction cannot publish that late write.
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		changed, err := s.queries.WithTx(tx).ConnectRuntimeNode(ctx, sqlc.ConnectRuntimeNodeParams{ID: id, ConnectionID: connection, OwnerEpoch: int64(epoch)})
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrRuntimeNodeCredential
		}
		return ctx.Err()
	})
}
func (s *Store) HeartbeatRuntimeNode(ctx context.Context, nodeID, connectionID string, epoch uint64, health RuntimeNodeHealth) error {
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return err
	}
	connection, err := parseConnectionGeneration(connectionID)
	if err != nil {
		return err
	}
	// A diagnostic explains unreadiness only. Unknown node values, including
	// arbitrary text, are stored as provider_unavailable; empty stays empty.
	health.Diagnostic = sandbox.NormalizeNodeDiagnostic(health.Diagnostic)
	if health.ProviderReady {
		health.Diagnostic = ""
	}
	for _, value := range []*int64{health.CPUCount, health.AvailableMemoryBytes, health.AvailableDiskBytes} {
		if value != nil && *value < 0 {
			return ErrInvalidInput
		}
	}
	if err := validateRuntimeNodeHost(health.Host); err != nil {
		return err
	}
	raw, err := json.Marshal(runtimeNodeHealthRecord{RuntimeNodeHealth: health, Host: health.Host})
	if err != nil {
		return err
	}
	changed, err := s.queries.HeartbeatRuntimeNode(ctx, sqlc.HeartbeatRuntimeNodeParams{ID: id, ConnectionID: connection, OwnerEpoch: int64(epoch), ProviderReady: health.ProviderReady, Health: raw})
	if err == nil && changed != 1 {
		return ErrRuntimeNodeCredential
	}
	return err
}
func (s *Store) DisconnectRuntimeNode(ctx context.Context, nodeID, connectionID string, epoch uint64) error {
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return err
	}
	connection, err := parseConnectionGeneration(connectionID)
	if err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		// A Connect COMMIT may be uncertain. Wait on the node regardless of
		// the visible connection, then fence cleanup in a fresh statement snapshot.
		if _, err := q.LockRuntimeNodePresence(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		if err := q.DisconnectRuntimeNode(ctx, sqlc.DisconnectRuntimeNodeParams{ID: id, ConnectionID: connection, OwnerEpoch: int64(epoch)}); err != nil {
			return err
		}
		return ctx.Err()
	})
}
