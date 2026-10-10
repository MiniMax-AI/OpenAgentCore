package deployment

import (
	"context"
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// EnsurePlacement commits replacement demand before entering a node lifecycle.
// Its reservation survives a failed caller, while the released allocation's
// immutable receipt continues to identify the previous compute owner.
func (e *ExecutionOperations) EnsurePlacement(ctx context.Context, key AllocationKey, installation string) (placement.Reserved, error) {
	key, err := key.parse()
	if err != nil {
		return placement.Reserved{}, err
	}
	installation, err = parseID(installation)
	if err != nil {
		return placement.Reserved{}, err
	}
	var result placement.Reserved
	err = e.storage.WithReservation(ctx, key, func(locked sessions.LockedSession, tx ReservationTx) error {
		if err := locked.Public(); err != nil {
			return err
		}
		existing, found, err := tx.FindAllocation()
		if err != nil {
			return err
		}
		if !found || existing.State != "released" {
			return ErrAllocationConflict
		}
		allowed, err := tx.CanReplaceAllocation()
		if err != nil {
			return err
		}
		if !allowed {
			return ErrAllocationConflict
		}
		d, err := tx.LockDeployment()
		if err != nil {
			return err
		}
		if err := e.service.rules.CheckAdmission(d, installation); err != nil {
			return err
		}
		if d.Mode != string(sandbox.DeploymentNodes) {
			return ErrAllocationConflict
		}
		reserved, err := tx.LoadReserved()
		if err != nil {
			return err
		}
		if !reserved.Released {
			if err := placement.CheckReserved(reserved); err != nil {
				return err
			}
			result = reserved
			return nil
		}
		nodes, err := tx.LoadNodes()
		if err != nil {
			return err
		}
		// Retained storage cannot move onto an owned-storage generation. Filter
		// before reserving, so an incompatible ready node never traps demand.
		compatible := make(map[uint64]bool)
		filtered := nodes[:0]
		for _, node := range nodes {
			if node.ReadyGeneration == nil {
				continue
			}
			generation := *node.ReadyGeneration
			accepts, known := compatible[generation]
			if !known {
				specification := d.Specification
				if generation != d.Generation {
					stored, err := tx.LoadGenerationSpecification(generation)
					if err != nil {
						return err
					}
					specification = stored.Specification
				}
				var spec sandbox.DeploymentSpec
				if err := json.Unmarshal(specification, &spec); err != nil {
					return err
				}
				accepts = spec.Workspace != nil
				compatible[generation] = accepts
			}
			if accepts {
				filtered = append(filtered, node)
			}
		}
		selected, err := e.service.rules.DecidePlacement(d, filtered)
		if err != nil {
			return err
		}
		if selected == nil {
			return ErrAllocationConflict
		}
		if err := tx.ReservePlacement(*selected); err != nil {
			return err
		}
		result = placement.Reserved{NodeID: selected.NodeID, Generation: selected.Generation, Available: true}
		return nil
	})
	return result, err
}
