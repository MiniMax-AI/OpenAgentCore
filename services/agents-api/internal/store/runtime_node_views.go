package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func (n RuntimeNode) MarshalJSON() ([]byte, error) {
	type plain RuntimeNode
	var active, retained *int
	if n.AdmissionState == "enabled" {
		active, retained = &n.MaxActive, &n.MaxRetained
	}
	return json.Marshal(struct {
		plain
		MaxActive   *int `json:"max_active"`
		MaxRetained *int `json:"max_retained"`
	}{plain(n), active, retained})
}

func runtimeNodeRevision(n sqlc.ListRuntimeNodesRow, d sqlc.RuntimeDeployment) string {
	return runtimeTokenDigest(fmt.Sprintf("%s:%d:%d:%s", runtimeUUID(n.ID), n.ConfigurationVersion, d.Generation, d.Specification))
}

func runtimeNodeView(n sqlc.ListRuntimeNodesRow, d sqlc.RuntimeDeployment, now time.Time) (RuntimeNode, error) {
	var seen *time.Time
	if n.LastSeenAt.Valid {
		value := n.LastSeenAt.Time
		seen = &value
	}
	var health RuntimeNodeHealth
	if err := json.Unmarshal(n.Health, &health); err != nil {
		return RuntimeNode{}, err
	}
	health.ProviderReady = n.ProviderReady
	view := RuntimeNode{RuntimeNodeHealth: health, Running: n.Running, Snapshots: n.Snapshots,
		ID: runtimeUUID(n.ID), Name: n.Name, Provider: n.ProviderKind, Online: n.Online, LastSeenAt: seen,
		MaxActive: int(n.MaxActive), MaxRetained: int(n.MaxRetained), Active: n.Active, Reserved: n.Reserved,
		Retained: n.Retained, CleanupPending: n.CleanupPending, CreatedAt: n.CreatedAt.Time,
		AdmissionState: n.AdmissionState, ConfigRevision: runtimeNodeRevision(n, d),
		deploymentGeneration: n.DeploymentGeneration, specificationDigest: n.SpecificationDigest}
	return enrichRuntimeNode(view, d, now), nil
}

func runtimeNodeViewByID(ctx context.Context, q *sqlc.Queries, d sqlc.RuntimeDeployment, id string) (RuntimeNode, sqlc.ListRuntimeNodesRow, error) {
	rows, err := q.ListRuntimeNodes(ctx)
	if err != nil {
		return RuntimeNode{}, sqlc.ListRuntimeNodesRow{}, err
	}
	for _, row := range rows {
		if runtimeUUID(row.ID) == id {
			view, err := runtimeNodeView(row, d, time.Now())
			return view, row, err
		}
	}
	return RuntimeNode{}, sqlc.ListRuntimeNodesRow{}, ErrNotFound
}

func (s *Store) ListRuntimeNodes(ctx context.Context) ([]RuntimeNode, error) {
	out := []RuntimeNode{}
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		d, err := q.GetRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		rows, err := q.ListRuntimeNodes(ctx)
		if err != nil {
			return err
		}
		now := time.Now()
		for _, row := range rows {
			view, err := runtimeNodeView(row, d, now)
			if err != nil {
				return err
			}
			out = append(out, view)
		}
		return nil
	})
	return out, err
}

func (s *Store) GetRuntimeNode(ctx context.Context, id string) (RuntimeNode, error) {
	if _, err := parseConnectionGeneration(id); err != nil {
		return RuntimeNode{}, err
	}
	var result RuntimeNode
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		d, err := q.GetRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		result, _, err = runtimeNodeViewByID(ctx, q, d, id)
		return err
	})
	return result, err
}
