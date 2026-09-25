package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func runtimeNodeUpdateDigest(input RuntimeNodeUpdate, d sqlc.RuntimeDeployment) string {
	raw, _ := json.Marshal(struct {
		Input         RuntimeNodeUpdate
		Generation    int64
		Specification json.RawMessage
	}{input, d.Generation, d.Specification})
	return runtimeTokenDigest(string(raw))
}

func (s *Store) UpdateRuntimeNode(ctx context.Context, nodeID string, input RuntimeNodeUpdate) (RuntimeNode, error) {
	if input.ExpectedConfigRevision == "" || (input.Name == nil && input.MaxActive == nil && input.MaxRetained == nil && input.AdmissionState == nil) || (input.AdmissionState != nil && *input.AdmissionState != "enabled") {
		return RuntimeNode{}, ErrInvalidInput
	}
	id, err := parseConnectionGeneration(nodeID)
	if err != nil {
		return RuntimeNode{}, err
	}
	var result RuntimeNode
	err = s.runtimeManagerTransaction(ctx, func(q *sqlc.Queries, d sqlc.RuntimeDeployment) error {
		if _, err := q.LockRuntimeNodePresence(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		current, row, err := runtimeNodeViewByID(ctx, q, d, nodeID)
		if err != nil {
			return err
		}
		digest := runtimeNodeUpdateDigest(input, d)
		if current.ConfigRevision != input.ExpectedConfigRevision {
			if row.LastUpdateRevision == input.ExpectedConfigRevision && row.LastUpdateDigest == digest {
				result = current
				return nil
			}
			return fmt.Errorf("%w: configuration_revision_changed", ErrRuntimeNodeConfigurationConflict)
		}
		name, active, retained, state := current.Name, current.MaxActive, current.MaxRetained, current.AdmissionState
		if input.Name != nil {
			name = *input.Name
		}
		if input.MaxActive != nil {
			active = *input.MaxActive
		}
		if input.MaxRetained != nil {
			retained = *input.MaxRetained
		}
		if input.AdmissionState != nil {
			state = *input.AdmissionState
		}
		activating := current.AdmissionState != "enabled" && state == "enabled"
		if activating {
			if input.MaxActive == nil {
				return ErrInvalidInput
			}
			if input.MaxRetained == nil {
				retained = max(active, runtimeNodeDefaultRetained)
			}
		} else if current.AdmissionState != "enabled" && (input.MaxActive != nil || input.MaxRetained != nil) {
			return ErrInvalidInput
		}
		if err := validateRuntimeNode(name, active, retained); err != nil {
			return err
		}
		if (active < current.MaxActive && int64(active) < current.Active) || (retained < current.MaxRetained && int64(retained) < current.Retained) {
			return fmt.Errorf("%w: retained_resources_exceed_limit", ErrRuntimeNodeConfigurationConflict)
		}
		increasing := active > current.MaxActive || retained > current.MaxRetained
		if activating || increasing {
			if d.Maintenance || !current.Online || !current.ProviderReady || !runtimeNodeMatchesDeployment(row.DeploymentGeneration, row.SpecificationDigest, d) {
				return fmt.Errorf("%w: node_unavailable", ErrRuntimeNodeConfigurationConflict)
			}
			if current.Capacity.Status != "ready" || current.Capacity.SelectableMaxActive == nil || active > *current.Capacity.SelectableMaxActive {
				return fmt.Errorf("%w: capacity_unavailable", ErrRuntimeNodeConfigurationConflict)
			}
			if reason := runtimeNodeFreeCapacityReason(current); reason != "" {
				return fmt.Errorf("%w: %s", ErrRuntimeNodeConfigurationConflict, reason)
			}
		}
		if name == current.Name && active == current.MaxActive && retained == current.MaxRetained && state == current.AdmissionState {
			result = current
			return nil
		}
		_, err = q.UpdateRuntimeNode(ctx, sqlc.UpdateRuntimeNodeParams{ID: id, Name: name, MaxActive: int32(active), MaxRetained: int32(retained), AdmissionState: state, LastUpdateRevision: input.ExpectedConfigRevision, LastUpdateDigest: digest})
		if err != nil {
			return err
		}
		result, _, err = runtimeNodeViewByID(ctx, q, d, nodeID)
		return err
	})
	return result, err
}
