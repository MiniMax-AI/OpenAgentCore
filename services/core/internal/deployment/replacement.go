package deployment

import (
	"context"
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// EnsurePlacement reserves first or retained demand before entering a node lifecycle.
// The immutable engine profile and actual selected generation are validated
// before committing its reservation.
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
		environment, err := tx.LoadEnvironment(ctx)
		if err != nil {
			return err
		}
		kind, err := sessions.EnvironmentType(environment.Configuration)
		if err != nil || kind != "openai_hosted" || environment.Status == "failed" || environment.Status == "expired" {
			return ErrAllocationConflict
		}
		if found {
			if existing.State != "released" {
				return ErrAllocationConflict
			}
			allowed, err := tx.CanReplaceAllocation()
			if err != nil {
				return err
			}
			if !allowed {
				return ErrAllocationConflict
			}
		} else if environment.Initialization != "pending" && environment.Initialization != "complete" {
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
		filtered, err := e.eligibleNodes(d, nodes, locked.Engine, found, tx.LoadGenerationSpecification)
		if err != nil {
			return err
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

// eligibleNodes applies one generation and Harness compatibility decision for
// allocation admission and capacity-pressure decisions. Retained files require
// another external-storage generation; new Sessions may select either kind.
func (e *ExecutionOperations) eligibleNodes(d placement.Deployment, nodes []placement.Node, engineKind string, retained bool, loadGeneration func(uint64) (GenerationSpecification, error)) ([]placement.Node, error) {
	profile, known := e.engines.Lookup(engineKind)
	if !known {
		return nil, sessions.ErrInvalidInput
	}
	compatible := make(map[uint64]bool)
	filtered := make([]placement.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.ReadyGeneration == nil {
			// Placement cannot select it, but preserves its preparing diagnosis.
			filtered = append(filtered, node)
			continue
		}
		generation := *node.ReadyGeneration
		accepts, known := compatible[generation]
		if !known {
			specification := d.Specification
			if generation != d.Generation {
				stored, err := loadGeneration(generation)
				if err != nil {
					return nil, err
				}
				specification = stored.Specification
			}
			var spec sandbox.DeploymentSpec
			if err := json.Unmarshal(specification, &spec); err != nil {
				return nil, err
			}
			accepts = (!retained || spec.Workspace != nil) && sessions.ValidateRetainedHistory(spec.Workspace != nil, profile.RetainedNativeHistory.IsSupported()) == nil
			compatible[generation] = accepts
		}
		if accepts {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}
