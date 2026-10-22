package deployment

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// freeSlotIdle is how long an allocation on a full node stays idle before
// Core may suspend it without waiting demand.
const freeSlotIdle = 2 * time.Minute

// SuspensionDemand is a transaction-bound capacity observation. It is not a
// reservation: only a confirmed suspension frees an active slot.
type SuspensionDemand struct {
	Deployment         placement.Deployment
	Nodes              []placement.Node
	InFlightNodes      []string
	StartingNodes      []string
	CheckpointRestores []CheckpointRestoreDemand
}

// CheckpointRestoreDemand carries one stopped owner and exact-generation target evidence.
type CheckpointRestoreDemand struct {
	SourceNode    string
	Compatibility sandbox.CheckpointCompatibility
	Nodes         []placement.CheckpointNode
}

// fullNode returns the owner's node when it is online and has no free active
// slot, in a nodes deployment that is not resetting and belongs to the owner's
// installation.
func fullNode(pressure SuspensionDemand, owner Allocation) *placement.Node {
	if pressure.Deployment.Resetting || pressure.Deployment.Mode != string(sandbox.DeploymentNodes) || pressure.Deployment.InstallationID != owner.ProviderKey {
		return nil
	}
	for i := range pressure.Nodes {
		if n := &pressure.Nodes[i]; n.ID == owner.NodeID && n.Online && n.Active >= int64(n.MaxActive) {
			return n
		}
	}
	return nil
}

// canKeepFreeSlot allows suspending an allocation that stayed idle for
// freeSlotIdle on a full node with no reclamation in flight. The node must
// also have a free retained slot, or the freed active slot admits no Session,
// and no Session still starting on it: the node runs one operation at a time,
// so that Session's create and preparation would wait for this suspension.
func canKeepFreeSlot(pressure SuspensionDemand, owner Allocation) bool {
	n := fullNode(pressure, owner)
	return n != nil && n.Retained < int64(n.MaxRetained) && !slices.Contains(pressure.InFlightNodes, owner.NodeID) && !slices.Contains(pressure.StartingNodes, owner.NodeID)
}

// canSuspendForDemand reports whether compatible waiting demand would place on
// the owner's full node once the owner is suspended.
func (e *ExecutionOperations) canSuspendForDemand(tx AllocationTx, pressure SuspensionDemand, owner Allocation) (bool, error) {
	source := fullNode(pressure, owner)
	if source == nil {
		return false, nil
	}
	for _, demand := range pressure.CheckpointRestores {
		if _, err := e.service.rules.ChooseCheckpoint(demand.Nodes, demand.SourceNode, demand.Compatibility, false); err == nil {
			continue
		} else if !errors.Is(err, placement.ErrNodeUnavailable) {
			return false, err
		}
		candidates := slices.Clone(demand.Nodes)
		for i := range candidates {
			if candidates[i].Node.ID == owner.NodeID {
				candidates[i].Node.Active--
			}
		}
		chosen, err := e.service.rules.ChooseCheckpoint(candidates, demand.SourceNode, demand.Compatibility, false)
		if err == nil && chosen == owner.NodeID {
			eligible := make([]placement.Node, 0, len(candidates))
			for _, candidate := range candidates {
				if candidate.Checkpoint != nil && *candidate.Checkpoint == demand.Compatibility {
					eligible = append(eligible, candidate.Node)
				}
			}
			inFlight, err := e.reclamationInFlight(pressure, eligible)
			if err != nil {
				return false, err
			}
			if !inFlight {
				return true, nil
			}
		} else if err != nil && !errors.Is(err, placement.ErrNodeUnavailable) {
			return false, err
		}
	}
	release := func(n *placement.Node) { n.Active-- }
	if source.Retained >= int64(source.MaxRetained) {
		// A full node frees a retained slot only by releasing a checkpoint
		// after this suspension, so the owner must itself be releasable.
		if retain, err := tx.CanRetainEnvironment(owner); err != nil || !retain {
			return false, err
		}
		release = func(n *placement.Node) { n.Active--; n.Retained-- }
	}
	return e.placementNeedsCapacity(tx, pressure, owner.NodeID, release)
}

// ExpireCheckpointForDemand ends the retention of an idle suspended
// allocation, exactly as if its deadline had passed, when compatible waiting
// demand is blocked only by the node's retained slots. It acts only on the
// node's oldest allocation whose Session can continue on new compute, so the
// ordinary expiry cleanup keeps files and native history. A refusal is
// ErrNotIdle.
func (e *ExecutionOperations) ExpireCheckpointForDemand(ctx context.Context, owner Allocation) error {
	_, err := e.change(ctx, owner, true, func(tx AllocationTx, current Allocation) (Allocation, error) {
		if current.State != "running" || current.ComputePhase != "suspended" || current.Expired || current.ComputeRevision != owner.ComputeRevision || current.NodeID == "" {
			return Allocation{}, ErrAllocationConflict
		}
		activity, err := tx.LoadActivity(current)
		if err != nil {
			return Allocation{}, err
		}
		if activity.Busy || activity.WakeRequested {
			return Allocation{}, ErrNotIdle
		}
		retain, err := tx.CanRetainEnvironment(current)
		if err != nil {
			return Allocation{}, err
		}
		if !retain {
			return Allocation{}, ErrNotIdle
		}
		oldest, err := tx.OldestIdleCheckpoint(current)
		if err != nil {
			return Allocation{}, err
		}
		if !oldest {
			return Allocation{}, ErrNotIdle
		}
		pressure, err := tx.LoadSuspensionDemand(current)
		if err != nil {
			return Allocation{}, err
		}
		var source *placement.Node
		for i := range pressure.Nodes {
			if pressure.Nodes[i].ID == current.NodeID {
				source = &pressure.Nodes[i]
			}
		}
		if pressure.Deployment.Resetting || pressure.Deployment.Mode != string(sandbox.DeploymentNodes) || pressure.Deployment.InstallationID != current.ProviderKey || source == nil || !source.Online || source.Retained < int64(source.MaxRetained) {
			return Allocation{}, ErrNotIdle
		}
		needed, err := e.placementNeedsCapacity(tx, pressure, current.NodeID, func(n *placement.Node) { n.Retained-- })
		if err != nil {
			return Allocation{}, err
		}
		if !needed {
			return Allocation{}, ErrNotIdle
		}
		return tx.ExpireCheckpoint(current)
	})
	return err
}

// ExpireUnusableCheckpoint ends the retention of a restoring allocation whose
// Provider reported its checkpoint unusable, exactly as if its deadline had
// passed. The snapshot is never restored again; ordinary expiry cleanup deletes
// it and any qualified Session continues on new compute.
func (e *ExecutionOperations) ExpireUnusableCheckpoint(ctx context.Context, owner Allocation) error {
	_, err := e.change(ctx, owner, false, func(tx AllocationTx, current Allocation) (Allocation, error) {
		if current.State != "running" || current.ComputePhase != "restoring" || current.Expired || current.ComputeRevision != owner.ComputeRevision {
			return Allocation{}, ErrAllocationConflict
		}
		return tx.ExpireCheckpoint(current)
	})
	return err
}

// placementNeedsCapacity scans bounded pages under the caller's deployment
// lock and transaction deadline. It reports whether waiting demand would place
// on nodeID once release frees the slot the caller may reclaim; an ineligible
// receipt supplies no pressure.
func (e *ExecutionOperations) placementNeedsCapacity(tx AllocationTx, pressure SuspensionDemand, nodeID string, release func(*placement.Node)) (bool, error) {
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
					release(&nodes[i])
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
