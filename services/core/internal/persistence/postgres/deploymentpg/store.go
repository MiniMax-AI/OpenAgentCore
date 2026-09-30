// Package deploymentpg stores the sandbox deployment, its retained generations
// and its nodes in PostgreSQL. It seals the deployment's provider credential
// with the Core credential key, bound to the installation and generation.
package deploymentpg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// Store is the deployment storage and reader on pooled connections. It grants
// no execution authority.
type Store struct {
	pool   *pgunit.Pool
	cipher *credentialcrypto.Cipher
}

// New returns the deployment store. cipher is nil when Core has no credential
// key; a stored credential then reads as credentialcrypto.ErrUnavailable.
func New(pool *pgunit.Pool, cipher *credentialcrypto.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

var (
	_ deployment.Storage = (*Store)(nil)
	_ deployment.Reader  = (*Store)(nil)
)

// Execution is the deployment storage of the execution owner: every
// transaction runs on the connection that holds the execution lease.
type Execution struct {
	lease  *pgunit.Lease
	cipher *credentialcrypto.Cipher
}

// NewExecution binds deployment changes to the execution lease.
func NewExecution(lease *pgunit.Lease, cipher *credentialcrypto.Cipher) *Execution {
	return &Execution{lease: lease, cipher: cipher}
}

var _ deployment.ExecutionStorage = (*Execution)(nil)

func (e *Execution) WithDeployment(ctx context.Context, apply func(deployment.DeploymentTx) error) error {
	return translate(e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.LockRuntimeDeployment(ctx); err != nil {
			return err
		}
		return apply(&deploymentTx{unit: unit{ctx: ctx, q: q, locked: true}, cipher: e.cipher})
	}))
}

func (s *Store) WithNodes(ctx context.Context, apply func(deployment.NodeTx) error) error {
	return translate(s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.LockRuntimeDeployment(ctx); err != nil {
			return err
		}
		return apply(&nodeTx{unit{ctx: ctx, q: q, locked: true}})
	}))
}

func (s *Store) ReadNodes(ctx context.Context, apply func(deployment.NodeReads) error) error {
	return s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return apply(unit{ctx: ctx, q: sqlc.New(tx)})
	})
}

// Deployment bounds its read by the execution deadline: the execution owner
// reads the setup through it before provider work.
func (s *Store) Deployment(ctx context.Context) (deployment.Record, error) {
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	d, err := s.pool.Queries().GetRuntimeDeployment(ctx)
	if err != nil {
		return deployment.Record{}, err
	}
	return record(d, s.cipher, true), nil
}

func (s *Store) Snapshot(ctx context.Context) (deployment.Snapshot, error) {
	row, err := s.pool.Queries().GetSandboxDeploymentSnapshot(ctx)
	if err != nil {
		return deployment.Snapshot{}, err
	}
	return snapshot(row)
}

func (s *Store) OwnerEpoch(ctx context.Context) (uint64, error) {
	d, err := s.pool.Queries().GetRuntimeDeployment(ctx)
	if err != nil {
		return 0, err
	}
	return uint64(d.OwnerEpoch), nil
}

func (s *Store) Allocation(ctx context.Context, ref sandbox.Reference) (deployment.AllocationRecord, error) {
	tenant, err := parseID(ref.TenantID)
	if err != nil {
		return deployment.AllocationRecord{}, err
	}
	environment, err := parseID(ref.EnvironmentID)
	if err != nil {
		return deployment.AllocationRecord{}, err
	}
	var result deployment.AllocationRecord
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.GetRuntimeAllocation(ctx, sqlc.GetRuntimeAllocationParams{TenantID: tenant, EnvironmentID: environment})
		if errors.Is(err, pgx.ErrNoRows) {
			return deployment.ErrNotFound
		}
		if err != nil {
			return err
		}
		a := row.RuntimeAllocation
		d, err := q.GetRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		result = deployment.AllocationRecord{ID: uuidString(a.ID), Released: a.State == "released", InstallationID: uuidString(a.ProviderKey), Deployment: record(d, s.cipher, true)}
		if !a.DeploymentGeneration.Valid {
			return nil
		}
		result.Generation = uint64(a.DeploymentGeneration.Int64)
		if a.DeploymentGeneration.Int64 == d.Generation {
			return nil
		}
		g, err := q.GetSandboxGeneration(ctx, a.DeploymentGeneration.Int64)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		retained := generation(g)
		result.Retained = &retained
		return nil
	})
	if err != nil {
		return deployment.AllocationRecord{}, err
	}
	return result, nil
}

func (s *Store) Generations(ctx context.Context, after int64) ([]deployment.GenerationRecord, error) {
	rows, err := s.pool.Queries().ListSandboxGenerations(ctx, after)
	if err != nil {
		return nil, err
	}
	out := make([]deployment.GenerationRecord, 0, len(rows))
	for _, g := range rows {
		out = append(out, generation(g))
	}
	return out, nil
}

func (s *Store) Nodes(ctx context.Context) ([]deployment.NodeRecord, error) {
	rows, err := s.pool.Queries().ListRuntimeNodes(ctx, pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	return nodeRecords(rows)
}

func (s *Store) NodeHistory(ctx context.Context, nodeID string, window coremetrics.Range) (deployment.NodeRecord, []deployment.HostHistoryPoint, error) {
	id, err := parseID(nodeID)
	if err != nil {
		return deployment.NodeRecord{}, nil, err
	}
	var node deployment.NodeRecord
	var points []deployment.HostHistoryPoint
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		rows, err := q.ListRuntimeNodes(ctx, id)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return deployment.ErrNotFound
		}
		nodes, err := nodeRecords(rows)
		if err != nil {
			return err
		}
		node = nodes[0]
		samples, err := q.ListNodeHostHistory(ctx, sqlc.ListNodeHostHistoryParams{
			NodeID: id, StartAt: pgtype.Timestamptz{Time: window.Start, Valid: true}, EndAt: pgtype.Timestamptz{Time: window.End, Valid: true},
			BucketWidth: pgtype.Interval{Microseconds: window.ResolutionSeconds * 1_000_000, Valid: true},
		})
		if err != nil {
			return err
		}
		points = make([]deployment.HostHistoryPoint, 0, len(samples))
		for _, sample := range samples {
			point := deployment.HostHistoryPoint{Start: sample.Start.Time}
			if sample.CpuSamples > 0 {
				point.CPUUtilizationMax = &sample.CpuUtilizationMax
			}
			if sample.MemorySamples > 0 {
				point.MemoryUsedBytesMax = &sample.MemoryUsedBytesMax
			}
			if sample.DiskSamples > 0 {
				point.AvailableDiskBytesMin = &sample.AvailableDiskBytesMin
			}
			points = append(points, point)
		}
		return nil
	})
	if err != nil {
		return deployment.NodeRecord{}, nil, err
	}
	return node, points, nil
}

func (s *Store) ConnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) (bool, error) {
	id, err := parseID(nodeID)
	if err != nil {
		return false, err
	}
	connection, err := parseID(connectionID)
	if err != nil {
		return false, err
	}
	var connected bool
	// A canceled autocommit UPDATE may still finish on PostgreSQL after pgx
	// returns. An explicit transaction cannot publish that late write.
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		changed, err := sqlc.New(tx).ConnectRuntimeNode(ctx, sqlc.ConnectRuntimeNodeParams{ID: id, ConnectionID: connection, OwnerEpoch: int64(epoch)})
		if err != nil {
			return err
		}
		connected = changed == 1
		return ctx.Err()
	})
	if err != nil {
		return false, err
	}
	return connected, nil
}

func (s *Store) DisconnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) error {
	id, err := parseID(nodeID)
	if err != nil {
		return err
	}
	connection, err := parseID(connectionID)
	if err != nil {
		return err
	}
	return s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
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

func (s *Store) SampleHostHistory(ctx context.Context) (int64, error) {
	return s.pool.Queries().SampleNodeHostHistory(ctx)
}
