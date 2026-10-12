package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

// ReserveAllocation commits the allocation of the tenant's hosted Environment
// and its dedicated device together, before the provider creates compute.
// installation is the provider key the allocation is provisioned for, and
// credentialHash the SHA-256 digest of the device credential. An existing
// allocation for the same installation returns Replayed; only a fresh one
// authorizes the one Create call.
func (e *ExecutionOperations) ReserveAllocation(ctx context.Context, key AllocationKey, installation, credentialHash string) (Allocation, error) {
	key, err := key.parse()
	if err != nil {
		return Allocation{}, err
	}
	if installation, err = parseID(installation); err != nil {
		return Allocation{}, err
	}
	registration, err := sessions.NewDeviceRegistration("managed-runtime", credentialHash)
	if err != nil {
		return Allocation{}, fmt.Errorf("%w: SHA-256 credential digest required", ErrInvalidInput)
	}
	var result Allocation
	err = e.storage.WithReservation(ctx, key, func(locked sessions.LockedSession, tx ReservationTx) error {
		if err := locked.Public(); err != nil {
			return err
		}
		environment, err := tx.LoadEnvironment(ctx)
		if err != nil {
			return err
		}
		if kind, err := sessions.EnvironmentType(environment.Configuration); err != nil || kind != "openai_hosted" {
			return ErrInvalidInput
		}
		existing, found, err := tx.FindAllocation()
		if err != nil {
			return err
		}
		if found && existing.State != "released" {
			if existing.ProviderKey != installation {
				return ErrAllocationConflict
			}
			existing.Replayed = true
			result = existing
			return nil
		}
		if found {
			allowed, err := tx.CanReplaceAllocation()
			if err != nil {
				return err
			}
			if !allowed {
				return ErrAllocationConflict
			}
		}
		// Existing receipts replay first, so retries and cleanup continue
		// while admission is closed.
		d, err := tx.LockDeployment()
		if err != nil {
			return err
		}
		if err := e.service.rules.CheckAdmission(d, installation); err != nil {
			return err
		}
		if environment.Status == "failed" || environment.Status == "expired" {
			return ErrInvalidInput
		}
		allocation := NewAllocation{ID: uuid.NewString(), EnvironmentID: environment.ID, DeviceID: uuid.NewString(), ProviderKey: installation, Generation: d.Generation}
		if d.Mode == string(sandbox.DeploymentNodes) {
			reserved, err := tx.LoadReserved()
			if err != nil {
				return err
			}
			if err := placement.CheckReserved(reserved); err != nil {
				return err
			}
			allocation.NodeID, allocation.Generation = reserved.NodeID, reserved.Generation
		}
		device := sessions.ExecutionDevice{ID: allocation.DeviceID, Name: registration.Name, EnvironmentID: environment.ID}
		if err := sessions.CreateEnvironmentDevice(ctx, tx, device, registration.CredentialHash); err != nil {
			return err
		}
		result, err = tx.InsertAllocation(allocation)
		return err
	})
	if err != nil {
		return Allocation{}, err
	}
	return result, nil
}

// ObserveRunning records verified original compute identity. It neither
// marks the Environment connected nor qualifies native preparation.
func (e *ExecutionOperations) ObserveRunning(ctx context.Context, owner Allocation) (Allocation, error) {
	return e.change(ctx, owner, true, func(tx AllocationTx, current Allocation) (Allocation, error) {
		return tx.ObserveRunning(current)
	})
}

// CheckRunning returns the stored allocation while it is still the owner's
// running compute: its Session undeleted and bound to the allocation's
// device. It follows an authenticated connection and a successful provider
// observation and changes nothing.
func (e *ExecutionOperations) CheckRunning(ctx context.Context, owner Allocation) (Allocation, error) {
	return e.change(ctx, owner, true, func(_ AllocationTx, current Allocation) (Allocation, error) {
		if current.State != "running" {
			return Allocation{}, ErrAllocationConflict
		}
		return current, nil
	})
}

// SettleCreation records evidence that the original Create can no longer
// change resources. A timeout, missing compute or lost lease is not evidence.
func (e *ExecutionOperations) SettleCreation(ctx context.Context, owner Allocation) (Allocation, error) {
	return e.change(ctx, owner, false, func(tx AllocationTx, current Allocation) (Allocation, error) {
		if current.State == "released" {
			return current, nil
		}
		return tx.SettleCreation(current)
	})
}

// ReleaseAllocation follows successful cleanup of the owned compute and
// volumes. An unknown Create outcome keeps its cleanup record even when the
// compute is absent.
func (e *ExecutionOperations) ReleaseAllocation(ctx context.Context, owner Allocation) (Allocation, error) {
	return e.change(ctx, owner, false, func(tx AllocationTx, current Allocation) (Allocation, error) {
		if current.State == "released" {
			return current, nil
		}
		return tx.Release(current)
	})
}

// RequestCleanup revokes the allocation's authority before external
// reclamation and settles the owning Session: a deleted Session's work is
// cancelled, and a live Environment fails with the generic provisioning
// reason unless its compute expired. Cancellation requests do not prove that
// native work has stopped.
func (e *ExecutionOperations) RequestCleanup(ctx context.Context, owner Allocation) (Allocation, error) {
	return e.cleanup(ctx, owner, cleanupRequested)
}

// ReleaseAbsentCreation consumes provider proof that the original Create is
// settled and owns no resources: it requests cleanup and releases the
// allocation in one commit.
func (e *ExecutionOperations) ReleaseAbsentCreation(ctx context.Context, owner Allocation) (Allocation, error) {
	return e.cleanup(ctx, owner, creationAbsent)
}

// ReleaseLostCompute consumes the owning node's confirmed cleanup of the
// owner's settled running incarnation after it observed that incarnation
// absent, stopped or crashed. It revokes the device and releases the allocation in one commit
// only while the owner's compute revision is current and its Session can
// continue on new compute; otherwise it changes nothing. The Session, its
// pending input, native identity and filesystem stay.
func (e *ExecutionOperations) ReleaseLostCompute(ctx context.Context, owner Allocation) (Allocation, error) {
	return e.cleanup(ctx, owner, computeLost)
}

// cleanupEvidence is the provider proof a cleanup commit consumes.
type cleanupEvidence int

const (
	cleanupRequested cleanupEvidence = iota
	creationAbsent
	computeLost
)

func (e *ExecutionOperations) cleanup(ctx context.Context, owner Allocation, evidence cleanupEvidence) (Allocation, error) {
	key, err := owner.Key().parse()
	if err != nil {
		return Allocation{}, err
	}
	lost := evidence == computeLost
	var result Allocation
	err = e.storage.WithAllocationCleanup(ctx, key, func(tx AllocationCleanupTx) error {
		current, err := checkAllocation(tx, owner, lost)
		if err != nil {
			return err
		}
		if lost && (current.State != "running" || current.ComputePhase != "running" || current.ComputeRevision != owner.ComputeRevision || !current.CreateSettled) {
			return ErrAllocationConflict
		}
		if current.State == "released" {
			result = current
			return nil
		}
		if err := tx.RevokeDevice(current); err != nil {
			return err
		}
		// The revocation commits with the Session's settlement, which reads
		// the allocation's expiry after it.
		revoked, err := tx.LoadAllocation()
		if err != nil {
			return err
		}
		if revoked.SessionDeleted {
			err = sessions.CancelWork(ctx, tx)
		} else {
			retain := false
			if revoked.Expired || lost {
				retain, err = tx.CanRetainEnvironment(revoked)
				if err != nil {
					return err
				}
			}
			// Compute retention ends independently of the Session only when its
			// initialized filesystem and native history can continue on new compute.
			// Explicit archive/reset makes this locked eligibility check false.
			if !retain && lost {
				return ErrAllocationConflict
			}
			if !retain {
				err = sessions.TerminateEnvironment(ctx, tx, revoked.Expired, sessions.ProvisioningFailureReason, nil)
			}
		}
		if err != nil {
			return err
		}
		pending, err := tx.RequestCleanup(current)
		if err != nil || evidence == cleanupRequested {
			result = pending
			return err
		}
		settled, err := tx.SettleCreation(pending)
		if err != nil {
			return err
		}
		result, err = tx.Release(settled)
		return err
	})
	if err != nil {
		return Allocation{}, err
	}
	return result, nil
}

// SetCompute commits a compute phase before its external effects. The owner's
// revision and the Session lock fence a stale lifecycle observation. Moving
// running compute to quiescing requires idle compute and either the idle
// timeout or capacity pressure, rechecked in the transaction.
func (e *ExecutionOperations) SetCompute(ctx context.Context, owner Allocation, phase string, state json.RawMessage, retainedUntil *time.Time) (Allocation, error) {
	if owner.ComputePhase == "suspended" && phase == "restoring" || !computeTransition(owner.ComputePhase, phase) || !json.Valid(state) {
		return Allocation{}, ErrInvalidInput
	}
	if phase != "running" && (retainedUntil == nil || retainedUntil.IsZero()) {
		return Allocation{}, ErrInvalidInput
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(state, &object) != nil || object == nil {
		return Allocation{}, ErrInvalidInput
	}
	return e.change(ctx, owner, true, func(tx AllocationTx, current Allocation) (Allocation, error) {
		if current.ComputeRevision != owner.ComputeRevision || current.ComputePhase != owner.ComputePhase {
			return Allocation{}, ErrAllocationConflict
		}
		if (phase == "quiescing" && current.ComputePhase == "running") || (phase == "suspending" && current.ComputePhase == "quiescing") {
			activity, err := tx.LoadActivity(current)
			if err != nil {
				return Allocation{}, err
			}
			if activity.Busy || activity.WakeRequested {
				return Allocation{}, ErrAllocationConflict
			}
			if phase == "quiescing" {
				if current.ComputeActivityAt.After(owner.ComputeActivityAt) {
					return Allocation{}, ErrAllocationConflict
				}
				seconds, err := tx.LoadIdleSeconds()
				if err != nil {
					return Allocation{}, err
				}
				if !activity.ReadyToSuspend(time.Duration(seconds) * time.Second) {
					// A short quiet period also backs off a rejected daemon quiesce.
					if !activity.ReadyToSuspend(15*time.Second) || current.NodeID == "" {
						return Allocation{}, ErrNotIdle
					}
					allowed, err := e.canSuspendForDemand(tx, current)
					if err != nil {
						return Allocation{}, err
					}
					if !allowed {
						return Allocation{}, ErrNotIdle
					}
				}
			}
		}
		return tx.SetCompute(current, ComputeChange{Phase: phase, State: state, RetainedUntil: retainedUntil})
	})
}

// BeginRestore reserves a compatible destination before any native restore.
// The caller supplies a fresh restore intent without a target; the destination
// adapter derives the target after this commit. Snapshot portability proves
// that publication and source-local cleanup settled before suspension.
func (e *ExecutionOperations) BeginRestore(ctx context.Context, owner Allocation, compatibility sandbox.CheckpointCompatibility, state json.RawMessage) (Allocation, error) {
	var object map[string]json.RawMessage
	if compatibility.Validate() != nil || json.Unmarshal(state, &object) != nil || object == nil {
		return Allocation{}, ErrInvalidInput
	}
	return e.change(ctx, owner, true, func(tx AllocationTx, current Allocation) (Allocation, error) {
		if current.State != "running" || current.ComputePhase != "suspended" || current.ComputeRevision != owner.ComputeRevision || current.Expired || current.ComputeRetainedUntil == nil {
			return Allocation{}, ErrAllocationConflict
		}
		activity, err := tx.LoadActivity(current)
		if err != nil {
			return Allocation{}, err
		}
		if !activity.Busy && !activity.WakeRequested {
			return Allocation{}, ErrAllocationConflict
		}
		if current.NodeID == "" {
			return tx.SetCompute(current, ComputeChange{Phase: "restoring", State: state, RetainedUntil: current.ComputeRetainedUntil})
		}
		d, nodes, err := tx.LoadCheckpointPlacement(current)
		if err != nil {
			return Allocation{}, err
		}
		if d.Resetting {
			return Allocation{}, placement.ErrResetAdmission
		}
		if d.InstallationID != current.ProviderKey || d.Mode != string(sandbox.DeploymentNodes) {
			return Allocation{}, placement.ErrNodeUnavailable
		}
		node, err := e.service.rules.ChooseCheckpoint(nodes, current.NodeID, compatibility, false)
		if err != nil {
			return Allocation{}, err
		}
		return tx.MoveSuspended(current, node, ComputeChange{Phase: "restoring", State: state, RetainedUntil: current.ComputeRetainedUntil})
	})
}

// RelocateCleanup durably assigns a portable suspended artifact to a node that
// can delete it. An unknown restore remains pinned to its existing target.
func (e *ExecutionOperations) RelocateCleanup(ctx context.Context, owner Allocation, compatibility sandbox.CheckpointCompatibility) (Allocation, error) {
	if owner.NodeID == "" || compatibility.Validate() != nil {
		return Allocation{}, ErrInvalidInput
	}
	return e.change(ctx, owner, false, func(tx AllocationTx, current Allocation) (Allocation, error) {
		if current.State != "cleanup_pending" || current.ComputePhase != "suspended" || current.ComputeRevision != owner.ComputeRevision {
			return Allocation{}, ErrAllocationConflict
		}
		d, nodes, err := tx.LoadCheckpointPlacement(current)
		if err != nil {
			return Allocation{}, err
		}
		if d.InstallationID != current.ProviderKey || d.Mode != string(sandbox.DeploymentNodes) {
			return Allocation{}, placement.ErrNodeUnavailable
		}
		node, err := e.service.rules.ChooseCheckpoint(nodes, current.NodeID, compatibility, true)
		if err != nil {
			return Allocation{}, err
		}
		if node == current.NodeID {
			return current, nil
		}
		return tx.MoveSuspended(current, node, ComputeChange{Phase: current.ComputePhase, State: current.ComputeState, RetainedUntil: current.ComputeRetainedUntil})
	})
}

// RecordObservation records a diagnostic for the observed compute revision
// and state of a node-backed allocation. It never releases ownership, changes
// public readiness or authorizes replacement.
func (e *ExecutionOperations) RecordObservation(ctx context.Context, owner Allocation, diagnostic string) error {
	if !observationDiagnostics[diagnostic] {
		return ErrInvalidInput
	}
	if owner.NodeID == "" {
		return nil
	}
	_, err := e.change(ctx, owner, false, func(tx AllocationTx, current Allocation) (Allocation, error) {
		if current.ComputeRevision != owner.ComputeRevision || current.State != owner.State || current.State == "released" {
			return current, nil
		}
		return current, tx.RecordObservation(current, diagnostic)
	})
	return err
}

// ClearWake consumes the allocation's wake request observed with the given
// activity time. A newer request stays.
func (e *ExecutionOperations) ClearWake(ctx context.Context, owner Allocation, observed time.Time) error {
	id, err := parseID(owner.ID)
	if err != nil {
		return err
	}
	return e.storage.ClearWake(ctx, id, observed)
}

// change runs apply on the owner's allocation in its Session-locked
// transaction, after checkAllocation.
func (e *ExecutionOperations) change(ctx context.Context, owner Allocation, live bool, apply func(AllocationTx, Allocation) (Allocation, error)) (Allocation, error) {
	key, err := owner.Key().parse()
	if err != nil {
		return Allocation{}, err
	}
	var result Allocation
	err = e.storage.WithAllocation(ctx, key, func(tx AllocationTx) error {
		current, err := checkAllocation(tx, owner, live)
		if err != nil {
			return err
		}
		result, err = apply(tx, current)
		return err
	})
	if err != nil {
		return Allocation{}, err
	}
	return result, nil
}

// checkAllocation returns the stored allocation when it is still the owner's:
// the same allocation, device, installation and node. A live change also
// requires the Session undeleted and bound to the allocation's device.
func checkAllocation(tx AllocationTx, owner Allocation, live bool) (Allocation, error) {
	current, err := tx.LoadAllocation()
	if err != nil {
		return Allocation{}, err
	}
	if current.ID != owner.ID || current.DeviceID != owner.DeviceID || current.ProviderKey != owner.ProviderKey || current.NodeID != owner.NodeID {
		return Allocation{}, ErrAllocationConflict
	}
	if !live {
		return current, nil
	}
	if current.SessionDeleted {
		return Allocation{}, sessions.ErrNotFound
	}
	device, bound, err := tx.LoadSessionDevice()
	if err != nil {
		return Allocation{}, err
	}
	if !bound || device.ID != current.DeviceID || device.EnvironmentID != current.EnvironmentID {
		return Allocation{}, ErrAllocationConflict
	}
	return current, nil
}

// TouchActivity records live-compute activity of the tenant's Environment.
// Only operations that need live compute call it; history and published
// Artifact reads never do.
func (s *Service) TouchActivity(ctx context.Context, tenant, environment string) error {
	key, err := AllocationKey{TenantID: tenant, EnvironmentID: environment}.parse()
	if err != nil {
		return err
	}
	return s.storage.WithActivity(ctx, key, func(locked sessions.LockedSession, tx ActivityTx) error {
		if err := locked.Public(); err != nil {
			return err
		}
		return tx.TouchActivity()
	})
}

// LifecycleNode routes a hosted Environment to the node lifecycle that
// provisions it, before its allocation exists: empty without a node. An
// existing allocation must agree with the Session's immutable placement.
func (s *Service) LifecycleNode(ctx context.Context, tenant, environment string) (string, error) {
	key, err := AllocationKey{TenantID: tenant, EnvironmentID: environment}.parse()
	if err != nil {
		return "", err
	}
	p, err := s.reader.LifecyclePlacement(ctx, key)
	if err != nil {
		return "", err
	}
	if p.Provider == "" || p.Mode == string(sandbox.DeploymentDirect) {
		if p.PlacementNodeID != "" || p.AllocationNodeID != "" {
			return "", placement.ErrNodeUnavailable
		}
		return "", nil
	}
	if p.PlacementNodeID == "" || (p.AllocationID != "" && p.AllocationNodeID != p.PlacementNodeID) || (p.AllocationID == "" && p.PlacementReleased) {
		return "", placement.ErrNodeUnavailable
	}
	return p.PlacementNodeID, nil
}

// AllocationGeneration returns the node and generation an unreleased
// allocation was created on, including for a deleted Session. It never
// substitutes the deployment's target.
func (s *Service) AllocationGeneration(ctx context.Context, ref sandbox.Reference) (string, uint64, error) {
	a, err := s.reader.EnvironmentAllocation(ctx, AllocationKey{TenantID: ref.TenantID, EnvironmentID: ref.EnvironmentID})
	if err != nil {
		return "", 0, err
	}
	if a.State == "released" || a.ID != ref.AllocationID || a.NodeID == "" || a.DeploymentGeneration == 0 {
		return "", 0, placement.ErrNodeUnavailable
	}
	return a.NodeID, a.DeploymentGeneration, nil
}
