package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

// RuntimeAllocation retains compute ownership, not public readiness. It survives
// Session deletion until cleanup is confirmed. No bootstrap secret is retained.
type RuntimeAllocation struct {
	NodeID                                                        string
	ObservationError                                              string
	ComputePhase                                                  string
	ComputeRevision                                               int64
	ComputeState                                                  json.RawMessage
	ComputeActivityAt                                             time.Time
	ComputeWakeRequested                                          bool
	ComputeRetainedUntil                                          *time.Time
	ID, EnvironmentID, SessionID, TenantID, DeviceID, ProviderKey string
	Initialization                                                string
	State                                                         string
	CreateSettled, SessionDeleted, Replayed, Expired              bool
	CreatedAt, KeptAt                                             time.Time
}

// RuntimeObservationSession is the minimum durable Core identity needed by the
// deployment-wide read-only sampler. Provider identity is resolved again by the
// observation service before any external read.
type RuntimeObservationSession struct {
	TenantID, SessionID string
}

type RuntimeObservationSessionPage struct {
	Sessions   []RuntimeObservationSession
	NextCursor string
}

// ReserveRuntimeAllocation commits the allocation and dedicated device together
// before external Create. Only a fresh receipt authorizes that one Create call.
func (s *Store) ReserveRuntimeAllocation(ctx context.Context, tenant, environment, providerKey, credentialHash string) (RuntimeAllocation, error) {
	if s.executionLease == nil {
		return RuntimeAllocation{}, ErrInvalidInput
	}
	provider, err := parseConnectionGeneration(providerKey)
	if err != nil {
		return RuntimeAllocation{}, err
	}
	owned, err := s.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return RuntimeAllocation{}, err
	}
	var config struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(owned.Configuration, &config) != nil || config.Type != "openai_hosted" {
		return RuntimeAllocation{}, ErrInvalidInput
	}
	lookup, err := deviceLookup(tenant, environment)
	if err != nil {
		return RuntimeAllocation{}, err
	}
	device, err := newDeviceParams(lookup.TenantID, "managed-runtime", credentialHash)
	if err != nil {
		return RuntimeAllocation{}, err
	}
	var result RuntimeAllocation
	err = s.withPublicSession(ctx, tenant, owned.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		previous, err := q.GetRuntimeAllocation(ctx, sqlc.GetRuntimeAllocationParams{TenantID: lookup.TenantID, EnvironmentID: lookup.ID})
		if err == nil {
			if previous.RuntimeAllocation.ProviderKey != provider {
				return ErrIdempotencyConflict
			}
			result = runtimeAllocationFromRow(previous.RuntimeAllocation, session, lookup.TenantID, previous.DeletedAt, previous.Expired)
			result.Replayed = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := checkRuntimeDeploymentAdmission(ctx, q, providerKey); err != nil {
			return err
		}
		current, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: lookup.TenantID, ID: session})
		if err != nil {
			return err
		}
		if current.Environment.Status == "failed" || current.Environment.Status == "expired" {
			return ErrInvalidInput
		}
		var nodeID pgtype.UUID
		deployment, err := q.GetRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		if deployment.Mode == "nodes" {
			placement, err := q.GetRuntimePlacement(ctx, lookup.ID)
			if err != nil {
				return err
			}
			if placement.ReleasedAt.Valid || !placement.Available {
				return ErrRuntimeNodeUnavailable
			}
			nodeID = placement.NodeID
		}
		if err := createEnvironmentDevice(ctx, q, lookup, session, device); err != nil {
			return err
		}
		row, err := q.CreateRuntimeAllocation(ctx, sqlc.CreateRuntimeAllocationParams{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, EnvironmentID: lookup.ID,
			DeviceID: device.ID, ProviderKey: provider, NodeID: nodeID,
		})
		if err == nil {
			result = runtimeAllocationFromRow(row, session, lookup.TenantID, pgtype.Timestamptz{}, false)
		}
		return err
	})
	if err != nil {
		return RuntimeAllocation{}, err
	}
	return result, nil
}

// GetRuntimeAllocation is an internal cleanup lookup, including deleted Sessions.
func (s *Store) GetRuntimeAllocation(ctx context.Context, tenant, environment string) (RuntimeAllocation, error) {
	lookup, err := deviceLookup(tenant, environment)
	if err != nil {
		return RuntimeAllocation{}, err
	}
	row, err := s.queries.GetRuntimeAllocation(ctx, sqlc.GetRuntimeAllocationParams{TenantID: lookup.TenantID, EnvironmentID: lookup.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeAllocation{}, ErrNotFound
	}
	if err != nil {
		return RuntimeAllocation{}, err
	}
	return runtimeAllocationFromRow(row.RuntimeAllocation, row.SessionID, row.TenantID, row.DeletedAt, row.Expired), nil
}

// ListRuntimeAllocations retains unresolved cleanup in bounded recovery scans.
func (s *Store) ListRuntimeAllocations(ctx context.Context, after string) ([]RuntimeAllocation, error) {
	if err := s.CheckExecutionOwnership(ctx); err != nil {
		return nil, err
	}
	id := pgtype.UUID{Valid: true}
	if after != "" {
		var err error
		id, err = parseID(after)
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.queries.ListRuntimeAllocations(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]RuntimeAllocation, 0, len(rows))
	for _, row := range rows {
		result = append(result, runtimeAllocationFromRow(row.RuntimeAllocation, row.SessionID, row.TenantID, row.DeletedAt, row.Expired))
	}
	return result, nil
}

// ListRuntimeObservationSessions performs a deployment-wide, read-only keyset
// scan of live managed Session identities. It excludes deleted Sessions and
// released allocations; it does not acquire, renew, or mutate Runtime state.
func (s *Store) ListRuntimeObservationSessions(ctx context.Context, after string, limit int) (RuntimeObservationSessionPage, error) {
	if limit < 1 || limit > 100 {
		return RuntimeObservationSessionPage{}, ErrInvalidInput
	}
	id := pgtype.UUID{Valid: true}
	if after != "" {
		var err error
		id, err = parseID(after)
		if err != nil {
			return RuntimeObservationSessionPage{}, err
		}
	}
	rows, err := s.queries.ListRuntimeObservationSessions(ctx, sqlc.ListRuntimeObservationSessionsParams{ID: id, Limit: int32(limit + 1)})
	if err != nil {
		return RuntimeObservationSessionPage{}, err
	}
	page := RuntimeObservationSessionPage{Sessions: make([]RuntimeObservationSession, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Sessions = append(page.Sessions, RuntimeObservationSession{
			TenantID:  uuid.UUID(row.TenantID.Bytes).String(),
			SessionID: uuid.UUID(row.ID.Bytes).String(),
		})
	}
	return page, nil
}

func runtimeAllocationFromRow(row sqlc.RuntimeAllocation, session, tenant pgtype.UUID, deleted pgtype.Timestamptz, expired bool) RuntimeAllocation {
	var retainedUntil *time.Time
	if row.ComputeRetainedUntil.Valid {
		value := row.ComputeRetainedUntil.Time
		retainedUntil = &value
	}
	return RuntimeAllocation{
		NodeID: runtimeUUID(row.NodeID), ObservationError: row.ObservationError,
		ComputePhase: row.ComputePhase, ComputeRevision: row.ComputeRevision, ComputeState: row.ComputeState,
		ComputeActivityAt: row.ComputeActivityAt.Time, ComputeWakeRequested: row.ComputeWakeRequested, ComputeRetainedUntil: retainedUntil,
		ID: uuid.UUID(row.ID.Bytes).String(), EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(),
		SessionID: uuid.UUID(session.Bytes).String(), TenantID: uuid.UUID(tenant.Bytes).String(),
		DeviceID: uuid.UUID(row.DeviceID.Bytes).String(), ProviderKey: uuid.UUID(row.ProviderKey.Bytes).String(),
		Initialization: row.Initialization, State: row.State, CreateSettled: row.CreateSettled, SessionDeleted: deleted.Valid, Expired: expired,
		CreatedAt: row.CreatedAt.Time, KeptAt: row.KeptAt.Time,
	}
}

// UnallocatedHostedEnvironment is a committed resource awaiting service bootstrap.
// A missing allocation is distinct from an unknown outcome of an existing Create.
type UnallocatedHostedEnvironment struct {
	ID, TenantID string
}

func (s *Store) ListUnallocatedHostedEnvironments(ctx context.Context, after string) ([]UnallocatedHostedEnvironment, error) {
	if err := s.CheckExecutionOwnership(ctx); err != nil {
		return nil, err
	}
	id := pgtype.UUID{Valid: true}
	if after != "" {
		var err error
		id, err = parseID(after)
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.queries.ListUnallocatedHostedEnvironments(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]UnallocatedHostedEnvironment, 0, len(rows))
	for _, row := range rows {
		result = append(result, UnallocatedHostedEnvironment{ID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String()})
	}
	return result, nil
}
