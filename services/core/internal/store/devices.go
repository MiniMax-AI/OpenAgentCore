package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// CreateDevice is operator provisioning, not a tenant-facing registration API.
func (s *Store) CreateDevice(ctx context.Context, tenantID, name, credentialHash string) (sessions.ExecutionDevice, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	params, err := newDeviceParams(tenant, name, credentialHash)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	id, err := s.queries.CreateDevice(ctx, params)
	if err != nil {
		return sessions.ExecutionDevice{}, fmt.Errorf("create execution device: %w", err)
	}
	return sessions.ExecutionDevice{ID: uuid.UUID(id.Bytes).String(), Name: params.Name}, nil
}

func newDeviceParams(tenant pgtype.UUID, name, credentialHash string) (sqlc.CreateDeviceParams, error) {
	name = strings.TrimSpace(name)
	digest, err := hex.DecodeString(credentialHash)
	if err != nil || len(digest) != 32 || name == "" || len(name) > 256 {
		return sqlc.CreateDeviceParams{}, fmt.Errorf("%w: device name and SHA-256 credential digest required", sessions.ErrInvalidInput)
	}
	return sqlc.CreateDeviceParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant,
		Name: name, CredentialHash: pgtype.Text{String: hex.EncodeToString(digest), Valid: true}}, nil
}

// GetDeviceCredential is used only by the shared gateway's credential verifier.
// The standalone service does not assign a product WorkspaceID.
func (s *Store) GetDeviceCredential(ctx context.Context, deviceID string) (runtimedevice.Credential, bool, error) {
	id, err := parseID(deviceID)
	if err != nil {
		return runtimedevice.Credential{}, false, nil
	}
	row, err := s.queries.GetDeviceCredential(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimedevice.Credential{}, false, nil
	}
	if err != nil {
		return runtimedevice.Credential{}, false, err
	}
	return runtimedevice.Credential{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name,
		Type: runtimegateway.RuntimeTypeAgentDaemon, CredentialHash: row.CredentialHash, RuntimeNodeID: row.RuntimeNodeID, RuntimeAllocationID: row.RuntimeAllocationID}, true, nil
}

func (s *Store) RevokeDevice(ctx context.Context, tenantID, deviceID string) error {
	params, err := deviceLookup(tenantID, deviceID)
	if err != nil {
		return err
	}
	n, err := s.queries.RevokeDevice(ctx, sqlc.RevokeDeviceParams(params))
	if err == nil && n == 0 {
		return sessions.ErrNotFound
	}
	return err
}

// BindSessionDevice keeps retries stable and refuses silent filesystem moves.
// Dispatchers must obtain the full Session binding before delivery.
func (s *Store) BindSessionDevice(ctx context.Context, tenantID, sessionID, deviceID string) error {
	params, err := deviceLookup(tenantID, deviceID)
	if err != nil {
		return err
	}
	return s.withSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		if _, err := q.GetDevice(ctx, params); errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		} else if err != nil {
			return err
		}
		_, err := q.BindSessionDevice(ctx, sqlc.BindSessionDeviceParams{TenantID: params.TenantID, ID: session, ID_2: params.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrDeviceBindingConflict
		}
		return err
	})
}

func (s *Store) GetSessionDevice(ctx context.Context, tenantID, sessionID string) (sessions.ExecutionDevice, error) {
	params, err := deviceLookup(tenantID, sessionID)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	if err := s.requireInitializedEnvironment(ctx, params.TenantID, params.ID); err != nil {
		return sessions.ExecutionDevice{}, err
	}
	return s.GetSessionRuntimeDevice(ctx, tenantID, sessionID)
}

// GetSessionRuntimeDevice reports an authorized connection binding. It does not
// admit native execution or file access before Environment preparation completes.
func (s *Store) GetSessionRuntimeDevice(ctx context.Context, tenantID, sessionID string) (sessions.ExecutionDevice, error) {
	params, err := deviceLookup(tenantID, sessionID)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	row, err := s.queries.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams(params))
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ExecutionDevice{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	return executionDevice(row.ID, row.Name, row.EnvironmentID), nil
}

func (s *Store) GetSessionExecutionBinding(ctx context.Context, tenantID, sessionID string) (sessions.ExecutionBinding, error) {
	params, err := deviceLookup(tenantID, sessionID)
	if err != nil {
		return sessions.ExecutionBinding{}, err
	}
	if err := s.requireInitializedEnvironment(ctx, params.TenantID, params.ID); err != nil {
		return sessions.ExecutionBinding{}, err
	}
	row, err := s.queries.GetSessionExecutionBinding(ctx, sqlc.GetSessionExecutionBindingParams(params))
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ExecutionBinding{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.ExecutionBinding{}, err
	}
	return sessions.ExecutionBinding{
		Device:          executionDevice(row.ID, row.Name, row.EnvironmentID),
		NativeSessionID: row.NativeSessionID,
		HasStartedTurn:  row.HasStartedTurn,
	}, nil
}

func executionDevice(id pgtype.UUID, name string, environmentID pgtype.UUID) sessions.ExecutionDevice {
	value := sessions.ExecutionDevice{ID: uuid.UUID(id.Bytes).String(), Name: name}
	if environmentID.Valid {
		value.EnvironmentID = uuid.UUID(environmentID.Bytes).String()
	}
	return value
}

func deviceLookup(tenantID, id string) (sqlc.GetDeviceParams, error) {
	var p sqlc.GetDeviceParams
	var err error
	if p.TenantID, err = parseID(tenantID); err != nil {
		return p, err
	}
	p.ID, err = parseID(id)
	return p, err
}

func (s *Store) TouchRuntimeHeartbeat(ctx context.Context, deviceID string) (runtimedevice.HeartbeatStatus, error) {
	id, err := parseID(deviceID)
	if err != nil {
		return runtimedevice.HeartbeatStatus{}, err
	}
	n, err := s.queries.TouchDevice(ctx, id)
	return runtimedevice.HeartbeatStatus{Liveness: "online", Deleted: n == 0}, err
}

func (s *Store) TouchAgentDaemonHeartbeat(ctx context.Context, heartbeat runtimedevice.Heartbeat) (runtimedevice.HeartbeatStatus, error) {
	id, err := parseID(heartbeat.RuntimeID)
	if err != nil {
		return runtimedevice.HeartbeatStatus{}, err
	}
	n, err := s.queries.TouchAuthenticatedDevice(ctx, sqlc.TouchAuthenticatedDeviceParams{
		ID: id, CredentialHash: heartbeat.CredentialHash,
	})
	return runtimedevice.HeartbeatStatus{Liveness: "online", Deleted: n == 0}, err
}

// Live connectivity belongs to the gateway Registry. Only last-seen time is
// persisted, so a stale socket closing cannot overwrite a newer connection.
func (s *Store) MarkRuntimeOffline(context.Context, string) error { return nil }
