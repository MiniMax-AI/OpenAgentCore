package deployment

import (
	"encoding/json"
	"time"
)

// Allocation retains a hosted Environment's compute ownership, not its public
// readiness. It survives Session deletion until cleanup is confirmed and keeps
// no bootstrap secret.
type Allocation struct {
	DeploymentGeneration uint64
	// NodeID is empty for an allocation no node serves.
	NodeID               string
	ObservationError     string
	ComputePhase         string
	ComputeRevision      int64
	ComputeState         json.RawMessage
	ComputeActivityAt    time.Time
	ComputeWakeRequested bool
	ComputeRetainedUntil *time.Time
	ID, EnvironmentID    string
	SessionID, TenantID  string
	DeviceID             string
	// ProviderKey is the installation the allocation was provisioned for.
	ProviderKey string
	State       string
	// CreateSettled records evidence that the original Create can no longer
	// change resources.
	CreateSettled bool
	// SessionDeleted reports that the owning Session was publicly deleted.
	SessionDeleted bool
	// Replayed reports that ReserveAllocation returned an existing allocation,
	// which never authorizes another Create.
	Replayed bool
	// Expired reports, by the database clock, that the allocation's lease or
	// retention has passed.
	Expired           bool
	CreatedAt, KeptAt time.Time
}

// Key names the allocation's Environment.
func (a Allocation) Key() AllocationKey {
	return AllocationKey{TenantID: a.TenantID, EnvironmentID: a.EnvironmentID}
}

// AllocationKey names a tenant's hosted Environment, which has at most one
// allocation.
type AllocationKey struct{ TenantID, EnvironmentID string }

// parse returns the key in canonical form, and ErrInvalidInput for a
// malformed identifier.
func (k AllocationKey) parse() (AllocationKey, error) {
	tenant, err := parseID(k.TenantID)
	if err != nil {
		return AllocationKey{}, err
	}
	environment, err := parseID(k.EnvironmentID)
	if err != nil {
		return AllocationKey{}, err
	}
	return AllocationKey{TenantID: tenant, EnvironmentID: environment}, nil
}

// NewAllocation is an allocation to store with its dedicated device.
type NewAllocation struct {
	ID, EnvironmentID, DeviceID, ProviderKey string
	// NodeID is empty when no node serves the allocation.
	NodeID     string
	Generation uint64
}

// SessionDevice is the Runtime device a Session is bound to.
type SessionDevice struct{ ID, EnvironmentID string }

// Activity separates real work from the connection keepalive. ObservedAt is
// the database clock when it was read.
type Activity struct {
	LastActivity, ObservedAt time.Time
	Busy, WakeRequested      bool
}

// ReadyToSuspend evaluates the idle policy on one database-clock observation.
func (a Activity) ReadyToSuspend(idleTimeout time.Duration) bool {
	return idleTimeout > 0 && !a.Busy && !a.WakeRequested &&
		a.ObservedAt.Sub(a.LastActivity) >= idleTimeout
}

// ComputeChange is the compute phase an operation commits before its external
// effects.
type ComputeChange struct {
	Phase string
	State json.RawMessage
	// RetainedUntil is nil only for the running phase.
	RetainedUntil *time.Time
}

// ObservationSession is the minimum durable identity the deployment-wide
// read-only sampler needs. The observation service resolves the provider
// identity again before any external read.
type ObservationSession struct{ TenantID, SessionID string }

// ObservationSessionPage is one page of ObservationSessions. NextCursor is
// empty on the last page.
type ObservationSessionPage struct {
	Sessions   []ObservationSession
	NextCursor string
}

// UnallocatedEnvironment is a committed hosted Environment awaiting its
// allocation. A missing allocation differs from an unknown Create outcome of
// an existing one.
type UnallocatedEnvironment struct{ ID, TenantID string }

// LifecyclePlacement is what routing a hosted Environment to its node
// lifecycle reads: the deployment's provider and mode, the Environment's
// allocation and the node placement its Session reserved.
type LifecyclePlacement struct {
	Provider, Mode string
	// AllocationID and AllocationNodeID are empty without an allocation or
	// its node.
	AllocationID, AllocationNodeID string
	// PlacementNodeID is empty without a placement.
	PlacementNodeID   string
	PlacementReleased bool
}

// NodeAllocation is an unreleased allocation a node serves.
type NodeAllocation struct {
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

// observationDiagnostics are the diagnostics an observation records; empty
// clears the previous one.
var observationDiagnostics = map[string]bool{
	"": true, "node_unavailable": true, "resource_missing": true,
	"compute_unconfirmed": true, "ownership_mismatch": true, "provider_unavailable": true,
}

// computeTransition reports whether managed compute may move from one phase
// to another. Repeating a phase records a new state, except while disabled.
func computeTransition(from, to string) bool {
	if from == to {
		return from != "disabled"
	}
	switch from {
	case "disabled":
		return to == "running"
	case "running":
		return to == "quiescing"
	case "quiescing":
		return to == "suspending" || to == "waking" || to == "running"
	case "suspending":
		return to == "suspended" || to == "waking"
	case "suspended":
		return to == "restoring"
	case "restoring":
		return to == "waking"
	case "waking":
		return to == "running"
	}
	return false
}
