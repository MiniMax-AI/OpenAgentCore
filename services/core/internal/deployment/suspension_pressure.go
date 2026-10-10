package deployment

import (
	"errors"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// SuspensionDemand is a transaction-bound capacity observation. It is not a
// reservation: only a confirmed suspension frees an active slot.
type SuspensionDemand struct {
	Deployment     placement.Deployment
	Nodes          []placement.Node
	InFlightNodes  []string
	RestoreWaiting bool
}

func (e *ExecutionOperations) canSuspendForDemand(tx AllocationTx, owner Allocation) (bool, error) {
	pressure, err := tx.LoadSuspensionDemand(owner)
	if err != nil {
		return false, err
	}
	if pressure.Deployment.Resetting || pressure.Deployment.Mode != string(sandbox.DeploymentNodes) || pressure.Deployment.InstallationID != owner.ProviderKey {
		return false, nil
	}
	var source *placement.Node
	for i := range pressure.Nodes {
		if pressure.Nodes[i].ID == owner.NodeID {
			source = &pressure.Nodes[i]
			break
		}
	}
	if source == nil || !source.Online || source.Active < int64(source.MaxActive) {
		return false, nil
	}
	// A retained restore already owns its retained slot and may use an older
	// prepared generation than the node's serving generation.
	if pressure.RestoreWaiting {
		return !slices.Contains(pressure.InFlightNodes, owner.NodeID), nil
	}
	reclaimRetained := source.Retained >= int64(source.MaxRetained)
	if reclaimRetained {
		retained, err := tx.CanRetainEnvironment(owner)
		if err != nil || !retained {
			return false, err
		}
	}
	return e.placementNeedsCapacity(tx, pressure, owner.NodeID, true, reclaimRetained)
}

// placementNeedsCapacity scans bounded pages under the caller's deployment
// lock and transaction deadline. Suspension and early retention expiry use the
// same admission decision; an ineligible old receipt never supplies pressure.
func (e *ExecutionOperations) placementNeedsCapacity(tx AllocationTx, pressure SuspensionDemand, nodeID string, freeActive, freeRetained bool) (bool, error) {
	var after PlacementDemandCursor
	for {
		demands, next, err := tx.PlacementDemand(after)
		if err != nil {
			return false, err
		}
		for _, demand := range demands {
			nodes, err := e.eligibleNodes(pressure.Deployment, pressure.Nodes, demand.Engine, demand.Retained, tx.LoadGenerationSpecification)
			if err != nil {
				return false, err
			}
			if _, err := e.service.rules.DecidePlacement(pressure.Deployment, nodes); err == nil {
				// Let the demand scan consume real free capacity first.
				return false, nil
			} else if !errors.Is(err, placement.ErrNodeUnavailable) && !errors.Is(err, placement.ErrNodesPreparing) {
				return false, err
			}
			inFlight, err := e.reclamationInFlight(pressure, nodes)
			if err != nil {
				return false, err
			}
			if inFlight {
				continue
			}
			for i := range nodes {
				if nodes[i].ID == nodeID {
					if freeActive {
						nodes[i].Active--
					}
					if freeRetained && nodes[i].Retained >= int64(nodes[i].MaxRetained) {
						nodes[i].Retained--
					}
				}
			}
			chosen, err := e.service.rules.DecidePlacement(pressure.Deployment, nodes)
			if err == nil && chosen != nil && chosen.NodeID == nodeID {
				return true, nil
			}
			if err != nil && !errors.Is(err, placement.ErrNodeUnavailable) && !errors.Is(err, placement.ErrNodesPreparing) {
				return false, err
			}
		}
		if next.EnvironmentID == "" {
			return false, nil
		}
		after = next
	}
}

// reclamationInFlight coordinates only resources whose nodes can serve this
// demand. Unavailable or incompatible nodes retain their receipts without
// stopping pressure recovery on a healthy node.
func (e *ExecutionOperations) reclamationInFlight(pressure SuspensionDemand, nodes []placement.Node) (bool, error) {
	for _, node := range nodes {
		if !slices.Contains(pressure.InFlightNodes, node.ID) {
			continue
		}
		// Capacity is what the in-flight operation may release; all other
		// placement requirements must hold now, using the normal admission rule.
		node.Active, node.Retained = 0, 0
		if chosen, err := e.service.rules.DecidePlacement(pressure.Deployment, []placement.Node{node}); err == nil && chosen != nil {
			return true, nil
		} else if err != nil && !errors.Is(err, placement.ErrNodeUnavailable) && !errors.Is(err, placement.ErrNodesPreparing) {
			return false, err
		}
	}
	return false, nil
}
