package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type RuntimeNodeAllocation struct {
	DeploymentGeneration uint64 `json:"deployment_generation"`
	Diagnostic           string `json:"diagnostic"`
	ID                   string `json:"id"`
	NodeID               string `json:"node_id"`
	TenantID             string `json:"tenant_id"`
	SessionID            string `json:"session_id"`
	EnvironmentID        string `json:"environment_id"`
	State                string `json:"state"`
	ComputePhase         string `json:"compute_phase"`
	// The time the allocation entered its current compute_phase, or null when unknown; an allocation that existed before Core recorded it reports null until its next phase change. For a suspended microsandbox allocation, this time plus the deployment's snapshot retention tells roughly when Core reclaims it.
	ComputePhaseChangedAt *time.Time `json:"compute_phase_changed_at" extensions:"x-nullable"`
	Initialization        string     `json:"initialization"`
	CreatedAt             time.Time  `json:"created_at"`
}

func runtimeUUID(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func (s *Store) ListNodeRuntimeAllocations(ctx context.Context, nodeID string) ([]RuntimeNodeAllocation, error) {
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return nil, err
	}
	if _, err := s.queries.GetRuntimeNode(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return nil, sessions.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListNodeRuntimeAllocations(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]RuntimeNodeAllocation, 0, len(rows))
	for _, a := range rows {
		var phaseChanged *time.Time
		if a.ComputePhaseChangedAt.Valid {
			value := a.ComputePhaseChangedAt.Time
			phaseChanged = &value
		}
		out = append(out, RuntimeNodeAllocation{DeploymentGeneration: uint64(a.DeploymentGeneration.Int64), Diagnostic: a.ObservationError, ID: runtimeUUID(a.ID), NodeID: runtimeUUID(a.NodeID), TenantID: runtimeUUID(a.TenantID), SessionID: runtimeUUID(a.SessionID), EnvironmentID: runtimeUUID(a.EnvironmentID), State: a.State, ComputePhase: a.ComputePhase, ComputePhaseChangedAt: phaseChanged, Initialization: a.Initialization, CreatedAt: a.CreatedAt.Time})
	}
	return out, nil
}
