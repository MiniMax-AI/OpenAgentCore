package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// fixturePublicURL is the installation public URL of these tests. E2B requires
// a public origin, so it is never loopback.
const fixturePublicURL = "https://core.example"

// fixtureRules are the placement rules of these tests, on fixturePublicURL.
func fixtureRules(t *testing.T) *placement.Rules {
	t.Helper()
	rules, err := placement.NewRules(providers.Builtin(), fixturePublicURL)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// testDeployment builds the pooled deployment service and reader and the
// deployment execution operations on lease, as cmd/server does for the Worker.
// cipher is nil when the owner has no credential key.
func testDeployment(t *testing.T, pool *pgxpool.Pool, cipher *credentialcrypto.Cipher, lease *pgunit.Lease) (*deployment.Service, deployment.Reader, *deployment.ExecutionOperations) {
	t.Helper()
	adapter := deploymentpg.New(pgunit.NewPool(pool), cipher)
	service, operations := deploymentOperations(t, adapter, adapter, deploymentpg.NewExecution(lease, cipher))
	return service, adapter, operations
}

// unitDeploymentService builds a deployment service for tests without a
// database. Only SetupForSelection, which never reads or writes, works; any
// storage call fails the test.
func unitDeploymentService(t *testing.T) *deployment.Service {
	t.Helper()
	service, _ := deploymentOperations(t, &strictDeploymentStorage{t: t}, &strictDeploymentReader{t: t}, &strictExecutionStorage{t: t})
	return service
}

// deploymentOperations builds the deployment service on storage and reader and
// the execution operations on execution.
func deploymentOperations(t *testing.T, storage deployment.Storage, reader deployment.Reader, execution deployment.ExecutionStorage) (*deployment.Service, *deployment.ExecutionOperations) {
	t.Helper()
	service, err := deployment.NewService(storage, reader, providers.Builtin(), fixtureRules(t))
	if err != nil {
		t.Fatal(err)
	}
	operations, err := deployment.NewExecutionOperations(service, execution)
	if err != nil {
		t.Fatal(err)
	}
	return service, operations
}

// unexpectedDeploymentCall fails the test for a storage call it did not set.
func unexpectedDeploymentCall(t *testing.T, name string) error {
	t.Helper()
	t.Error("unexpected call to " + name)
	return errors.New("unexpected call to " + name)
}

// strictDeploymentStorage runs each set func; any other call fails the test.
type strictDeploymentStorage struct {
	t                 *testing.T
	withNodes         func(context.Context, func(deployment.NodeTx) error) error
	connectNode       func(context.Context, string, string, uint64) (bool, error)
	disconnectNode    func(context.Context, string, string, uint64) error
	sampleHostHistory func(context.Context) (int64, error)
	withActivity      func(context.Context, deployment.AllocationKey, func(sessions.LockedSession, deployment.ActivityTx) error) error
}

func (s *strictDeploymentStorage) WithNodes(ctx context.Context, apply func(deployment.NodeTx) error) error {
	if s.withNodes == nil {
		return unexpectedDeploymentCall(s.t, "WithNodes")
	}
	return s.withNodes(ctx, apply)
}

func (s *strictDeploymentStorage) ConnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) (bool, error) {
	if s.connectNode == nil {
		return false, unexpectedDeploymentCall(s.t, "ConnectNode")
	}
	return s.connectNode(ctx, nodeID, connectionID, epoch)
}

func (s *strictDeploymentStorage) DisconnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) error {
	if s.disconnectNode == nil {
		return unexpectedDeploymentCall(s.t, "DisconnectNode")
	}
	return s.disconnectNode(ctx, nodeID, connectionID, epoch)
}

func (s *strictDeploymentStorage) SampleHostHistory(ctx context.Context) (int64, error) {
	if s.sampleHostHistory == nil {
		return 0, unexpectedDeploymentCall(s.t, "SampleHostHistory")
	}
	return s.sampleHostHistory(ctx)
}

// strictExecutionStorage runs withDeployment when set; otherwise any call
// fails the test.
type strictExecutionStorage struct {
	t                     *testing.T
	withDeployment        func(context.Context, func(deployment.DeploymentTx) error) error
	withReservation       func(context.Context, deployment.AllocationKey, func(sessions.LockedSession, deployment.ReservationTx) error) error
	withAllocation        func(context.Context, deployment.AllocationKey, func(deployment.AllocationTx) error) error
	withAllocationCleanup func(context.Context, deployment.AllocationKey, func(deployment.AllocationCleanupTx) error) error
	clearWake             func(context.Context, string, time.Time) error
}

func (s *strictExecutionStorage) WithDeployment(ctx context.Context, apply func(deployment.DeploymentTx) error) error {
	if s.withDeployment == nil {
		return unexpectedDeploymentCall(s.t, "WithDeployment")
	}
	return s.withDeployment(ctx, apply)
}

// strictDeploymentReader runs each set func; any other call fails the test.
type strictDeploymentReader struct {
	t                        *testing.T
	deployment               func(context.Context) (deployment.Record, error)
	snapshot                 func(context.Context) (deployment.Snapshot, error)
	ownerEpoch               func(context.Context) (uint64, error)
	allocation               func(context.Context, sandbox.Reference) (deployment.AllocationRecord, error)
	generations              func(context.Context, int64) ([]deployment.GenerationRecord, error)
	nodes                    func(context.Context) ([]deployment.NodeRecord, error)
	nodeHistory              func(context.Context, string, coremetrics.Range) (deployment.NodeRecord, []deployment.HostHistoryPoint, error)
	readNodes                func(context.Context, func(deployment.NodeReads) error) error
	resetSessions            func(context.Context, string, bool) ([]deployment.ResetSession, error)
	addressBindings          func(context.Context, string) (deployment.AddressBindings, error)
	environmentAllocation    func(context.Context, deployment.AllocationKey) (deployment.Allocation, error)
	credentialAllocations    func(context.Context, string) ([]deployment.Allocation, error)
	observationSessions      func(context.Context, string, int) (deployment.ObservationSessionPage, error)
	nodeAllocations          func(context.Context, string) ([]deployment.NodeAllocation, error)
	nodeOnline               func(context.Context, string) (bool, error)
	lifecycleNodes           func(context.Context) ([]string, error)
	lifecycleAllocations     func(context.Context, string, string) ([]deployment.Allocation, error)
	unallocatedEnvironments  func(context.Context, string, string) ([]deployment.UnallocatedEnvironment, error)
	lifecyclePlacement       func(context.Context, deployment.AllocationKey) (deployment.LifecyclePlacement, error)
	activity                 func(context.Context, string) (deployment.Activity, error)
	countComputeReservations func(context.Context, string) (int64, error)
	countRetainedAllocations func(context.Context, string) (int64, error)
}

func (r *strictDeploymentReader) Deployment(ctx context.Context) (deployment.Record, error) {
	if r.deployment == nil {
		return deployment.Record{}, unexpectedDeploymentCall(r.t, "Deployment")
	}
	return r.deployment(ctx)
}

func (r *strictDeploymentReader) Snapshot(ctx context.Context) (deployment.Snapshot, error) {
	if r.snapshot == nil {
		return deployment.Snapshot{}, unexpectedDeploymentCall(r.t, "Snapshot")
	}
	return r.snapshot(ctx)
}

func (r *strictDeploymentReader) OwnerEpoch(ctx context.Context) (uint64, error) {
	if r.ownerEpoch == nil {
		return 0, unexpectedDeploymentCall(r.t, "OwnerEpoch")
	}
	return r.ownerEpoch(ctx)
}

func (r *strictDeploymentReader) Allocation(ctx context.Context, ref sandbox.Reference) (deployment.AllocationRecord, error) {
	if r.allocation == nil {
		return deployment.AllocationRecord{}, unexpectedDeploymentCall(r.t, "Allocation")
	}
	return r.allocation(ctx, ref)
}

func (r *strictDeploymentReader) Generations(ctx context.Context, after int64) ([]deployment.GenerationRecord, error) {
	if r.generations == nil {
		return nil, unexpectedDeploymentCall(r.t, "Generations")
	}
	return r.generations(ctx, after)
}

func (r *strictDeploymentReader) Nodes(ctx context.Context) ([]deployment.NodeRecord, error) {
	if r.nodes == nil {
		return nil, unexpectedDeploymentCall(r.t, "Nodes")
	}
	return r.nodes(ctx)
}

func (r *strictDeploymentReader) NodeHistory(ctx context.Context, nodeID string, window coremetrics.Range) (deployment.NodeRecord, []deployment.HostHistoryPoint, error) {
	if r.nodeHistory == nil {
		return deployment.NodeRecord{}, nil, unexpectedDeploymentCall(r.t, "NodeHistory")
	}
	return r.nodeHistory(ctx, nodeID, window)
}

func (r *strictDeploymentReader) ReadNodes(ctx context.Context, apply func(deployment.NodeReads) error) error {
	if r.readNodes == nil {
		return unexpectedDeploymentCall(r.t, "ReadNodes")
	}
	return r.readNodes(ctx, apply)
}

func (r *strictDeploymentReader) ResetSessions(ctx context.Context, after string, force bool) ([]deployment.ResetSession, error) {
	if r.resetSessions == nil {
		return nil, unexpectedDeploymentCall(r.t, "ResetSessions")
	}
	return r.resetSessions(ctx, after, force)
}

func (r *strictDeploymentReader) AddressBindings(ctx context.Context, publicURL string) (deployment.AddressBindings, error) {
	if r.addressBindings == nil {
		return deployment.AddressBindings{}, unexpectedDeploymentCall(r.t, "AddressBindings")
	}
	return r.addressBindings(ctx, publicURL)
}

func (s *strictDeploymentStorage) WithActivity(ctx context.Context, key deployment.AllocationKey, apply func(sessions.LockedSession, deployment.ActivityTx) error) error {
	if s.withActivity == nil {
		return unexpectedDeploymentCall(s.t, "WithActivity")
	}
	return s.withActivity(ctx, key, apply)
}

func (s *strictExecutionStorage) WithReservation(ctx context.Context, key deployment.AllocationKey, apply func(sessions.LockedSession, deployment.ReservationTx) error) error {
	if s.withReservation == nil {
		return unexpectedDeploymentCall(s.t, "WithReservation")
	}
	return s.withReservation(ctx, key, apply)
}

func (s *strictExecutionStorage) WithAllocation(ctx context.Context, key deployment.AllocationKey, apply func(deployment.AllocationTx) error) error {
	if s.withAllocation == nil {
		return unexpectedDeploymentCall(s.t, "WithAllocation")
	}
	return s.withAllocation(ctx, key, apply)
}

func (s *strictExecutionStorage) WithAllocationCleanup(ctx context.Context, key deployment.AllocationKey, apply func(deployment.AllocationCleanupTx) error) error {
	if s.withAllocationCleanup == nil {
		return unexpectedDeploymentCall(s.t, "WithAllocationCleanup")
	}
	return s.withAllocationCleanup(ctx, key, apply)
}

func (s *strictExecutionStorage) ClearWake(ctx context.Context, allocationID string, observed time.Time) error {
	if s.clearWake == nil {
		return unexpectedDeploymentCall(s.t, "ClearWake")
	}
	return s.clearWake(ctx, allocationID, observed)
}

func (r *strictDeploymentReader) EnvironmentAllocation(ctx context.Context, key deployment.AllocationKey) (deployment.Allocation, error) {
	if r.environmentAllocation == nil {
		return deployment.Allocation{}, unexpectedDeploymentCall(r.t, "EnvironmentAllocation")
	}
	return r.environmentAllocation(ctx, key)
}

func (r *strictDeploymentReader) CredentialAllocations(ctx context.Context, after string) ([]deployment.Allocation, error) {
	if r.credentialAllocations == nil {
		return nil, unexpectedDeploymentCall(r.t, "CredentialAllocations")
	}
	return r.credentialAllocations(ctx, after)
}

func (r *strictDeploymentReader) ObservationSessions(ctx context.Context, after string, limit int) (deployment.ObservationSessionPage, error) {
	if r.observationSessions == nil {
		return deployment.ObservationSessionPage{}, unexpectedDeploymentCall(r.t, "ObservationSessions")
	}
	return r.observationSessions(ctx, after, limit)
}

func (r *strictDeploymentReader) NodeAllocations(ctx context.Context, nodeID string) ([]deployment.NodeAllocation, error) {
	if r.nodeAllocations == nil {
		return nil, unexpectedDeploymentCall(r.t, "NodeAllocations")
	}
	return r.nodeAllocations(ctx, nodeID)
}

func (r *strictDeploymentReader) NodeOnline(ctx context.Context, nodeID string) (bool, error) {
	if r.nodeOnline == nil {
		return false, unexpectedDeploymentCall(r.t, "NodeOnline")
	}
	return r.nodeOnline(ctx, nodeID)
}

func (r *strictDeploymentReader) LifecycleNodes(ctx context.Context) ([]string, error) {
	if r.lifecycleNodes == nil {
		return nil, unexpectedDeploymentCall(r.t, "LifecycleNodes")
	}
	return r.lifecycleNodes(ctx)
}

func (r *strictDeploymentReader) LifecycleAllocations(ctx context.Context, nodeID string, after string) ([]deployment.Allocation, error) {
	if r.lifecycleAllocations == nil {
		return nil, unexpectedDeploymentCall(r.t, "LifecycleAllocations")
	}
	return r.lifecycleAllocations(ctx, nodeID, after)
}

func (r *strictDeploymentReader) UnallocatedEnvironments(ctx context.Context, nodeID string, after string) ([]deployment.UnallocatedEnvironment, error) {
	if r.unallocatedEnvironments == nil {
		return nil, unexpectedDeploymentCall(r.t, "UnallocatedEnvironments")
	}
	return r.unallocatedEnvironments(ctx, nodeID, after)
}

func (r *strictDeploymentReader) LifecyclePlacement(ctx context.Context, key deployment.AllocationKey) (deployment.LifecyclePlacement, error) {
	if r.lifecyclePlacement == nil {
		return deployment.LifecyclePlacement{}, unexpectedDeploymentCall(r.t, "LifecyclePlacement")
	}
	return r.lifecyclePlacement(ctx, key)
}

func (r *strictDeploymentReader) Activity(ctx context.Context, allocationID string) (deployment.Activity, error) {
	if r.activity == nil {
		return deployment.Activity{}, unexpectedDeploymentCall(r.t, "Activity")
	}
	return r.activity(ctx, allocationID)
}

func (r *strictDeploymentReader) CountComputeReservations(ctx context.Context, installationID string) (int64, error) {
	if r.countComputeReservations == nil {
		return 0, unexpectedDeploymentCall(r.t, "CountComputeReservations")
	}
	return r.countComputeReservations(ctx, installationID)
}

func (r *strictDeploymentReader) CountRetainedAllocations(ctx context.Context, installationID string) (int64, error) {
	if r.countRetainedAllocations == nil {
		return 0, unexpectedDeploymentCall(r.t, "CountRetainedAllocations")
	}
	return r.countRetainedAllocations(ctx, installationID)
}
