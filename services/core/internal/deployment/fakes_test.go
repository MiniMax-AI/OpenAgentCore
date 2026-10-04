package deployment

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// The fakes are strict: each method runs its func field, and a nil field fails
// the test, so a test sets exactly the calls it expects.

func unexpected(t testing.TB, method string) {
	t.Helper()
	t.Fatalf("unexpected call to %s", method)
}

type fakeStorage struct {
	t                 testing.TB
	withNodes         func(context.Context, func(NodeTx) error) error
	connectNode       func(context.Context, string, string, uint64) (bool, error)
	disconnectNode    func(context.Context, string, string, uint64) error
	sampleHostHistory func(context.Context) (int64, error)
	withActivity      func(context.Context, AllocationKey, func(sessions.LockedSession, ActivityTx) error) error
}

func (f *fakeStorage) WithNodes(ctx context.Context, apply func(NodeTx) error) error {
	if f.withNodes == nil {
		unexpected(f.t, "WithNodes")
	}
	return f.withNodes(ctx, apply)
}

func (f *fakeStorage) ConnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) (bool, error) {
	if f.connectNode == nil {
		unexpected(f.t, "ConnectNode")
	}
	return f.connectNode(ctx, nodeID, connectionID, epoch)
}

func (f *fakeStorage) DisconnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) error {
	if f.disconnectNode == nil {
		unexpected(f.t, "DisconnectNode")
	}
	return f.disconnectNode(ctx, nodeID, connectionID, epoch)
}

func (f *fakeStorage) SampleHostHistory(ctx context.Context) (int64, error) {
	if f.sampleHostHistory == nil {
		unexpected(f.t, "SampleHostHistory")
	}
	return f.sampleHostHistory(ctx)
}

type fakeExecutionStorage struct {
	t                     testing.TB
	withDeployment        func(context.Context, func(DeploymentTx) error) error
	withReservation       func(context.Context, AllocationKey, func(sessions.LockedSession, ReservationTx) error) error
	withAllocation        func(context.Context, AllocationKey, func(AllocationTx) error) error
	withAllocationCleanup func(context.Context, AllocationKey, func(AllocationCleanupTx) error) error
	clearWake             func(context.Context, string, time.Time) error
}

func (f *fakeExecutionStorage) WithDeployment(ctx context.Context, apply func(DeploymentTx) error) error {
	if f.withDeployment == nil {
		unexpected(f.t, "WithDeployment")
	}
	return f.withDeployment(ctx, apply)
}

type fakeReader struct {
	t                        testing.TB
	deployment               func(context.Context) (Record, error)
	snapshot                 func(context.Context) (Snapshot, error)
	ownerEpoch               func(context.Context) (uint64, error)
	allocation               func(context.Context, sandbox.Reference) (AllocationRecord, error)
	generations              func(context.Context, int64) ([]GenerationRecord, error)
	nodes                    func(context.Context) ([]NodeRecord, error)
	nodeHistory              func(context.Context, string, coremetrics.Range) (NodeRecord, []HostHistoryPoint, error)
	readNodes                func(context.Context, func(NodeReads) error) error
	resetSessions            func(context.Context, string, bool) ([]ResetSession, error)
	addressBindings          func(context.Context, string) (AddressBindings, error)
	environmentAllocation    func(context.Context, AllocationKey) (Allocation, error)
	credentialAllocations    func(context.Context, string) ([]Allocation, error)
	observationSessions      func(context.Context, string, int) (ObservationSessionPage, error)
	nodeAllocations          func(context.Context, string) ([]NodeAllocation, error)
	nodeOnline               func(context.Context, string) (bool, error)
	lifecycleNodes           func(context.Context) ([]string, error)
	lifecycleAllocations     func(context.Context, string, string) ([]Allocation, error)
	unallocatedEnvironments  func(context.Context, string, string) ([]UnallocatedEnvironment, error)
	lifecyclePlacement       func(context.Context, AllocationKey) (LifecyclePlacement, error)
	activity                 func(context.Context, string) (Activity, error)
	countComputeReservations func(context.Context, string) (int64, error)
	countRetainedAllocations func(context.Context, string) (int64, error)
}

func (f *fakeReader) Deployment(ctx context.Context) (Record, error) {
	if f.deployment == nil {
		unexpected(f.t, "Deployment")
	}
	return f.deployment(ctx)
}

func (f *fakeReader) Snapshot(ctx context.Context) (Snapshot, error) {
	if f.snapshot == nil {
		unexpected(f.t, "Snapshot")
	}
	return f.snapshot(ctx)
}

func (f *fakeReader) OwnerEpoch(ctx context.Context) (uint64, error) {
	if f.ownerEpoch == nil {
		unexpected(f.t, "OwnerEpoch")
	}
	return f.ownerEpoch(ctx)
}

func (f *fakeReader) Allocation(ctx context.Context, ref sandbox.Reference) (AllocationRecord, error) {
	if f.allocation == nil {
		unexpected(f.t, "Allocation")
	}
	return f.allocation(ctx, ref)
}

func (f *fakeReader) Generations(ctx context.Context, after int64) ([]GenerationRecord, error) {
	if f.generations == nil {
		unexpected(f.t, "Generations")
	}
	return f.generations(ctx, after)
}

func (f *fakeReader) Nodes(ctx context.Context) ([]NodeRecord, error) {
	if f.nodes == nil {
		unexpected(f.t, "Nodes")
	}
	return f.nodes(ctx)
}

func (f *fakeReader) NodeHistory(ctx context.Context, nodeID string, window coremetrics.Range) (NodeRecord, []HostHistoryPoint, error) {
	if f.nodeHistory == nil {
		unexpected(f.t, "NodeHistory")
	}
	return f.nodeHistory(ctx, nodeID, window)
}

func (f *fakeReader) ReadNodes(ctx context.Context, apply func(NodeReads) error) error {
	if f.readNodes == nil {
		unexpected(f.t, "ReadNodes")
	}
	return f.readNodes(ctx, apply)
}

func (f *fakeReader) ResetSessions(ctx context.Context, after string, force bool) ([]ResetSession, error) {
	if f.resetSessions == nil {
		unexpected(f.t, "ResetSessions")
	}
	return f.resetSessions(ctx, after, force)
}

func (f *fakeReader) AddressBindings(ctx context.Context, publicURL string) (AddressBindings, error) {
	if f.addressBindings == nil {
		unexpected(f.t, "AddressBindings")
	}
	return f.addressBindings(ctx, publicURL)
}

type fakeNodeReads struct {
	t                           testing.TB
	loadDeployment              func() (Record, error)
	loadNode                    func(string) (StoredNode, error)
	loadEnrollment              func(string) (EnrollmentRecord, error)
	loadGenerationSpecification func(uint64) (GenerationSpecification, error)
	generationKept              func(string, uint64) (bool, error)
}

func (f *fakeNodeReads) LoadDeployment() (Record, error) {
	if f.loadDeployment == nil {
		unexpected(f.t, "LoadDeployment")
	}
	return f.loadDeployment()
}

func (f *fakeNodeReads) LoadNode(id string) (StoredNode, error) {
	if f.loadNode == nil {
		unexpected(f.t, "LoadNode")
	}
	return f.loadNode(id)
}

func (f *fakeNodeReads) LoadEnrollment(digest string) (EnrollmentRecord, error) {
	if f.loadEnrollment == nil {
		unexpected(f.t, "LoadEnrollment")
	}
	return f.loadEnrollment(digest)
}

func (f *fakeNodeReads) LoadGenerationSpecification(generation uint64) (GenerationSpecification, error) {
	if f.loadGenerationSpecification == nil {
		unexpected(f.t, "LoadGenerationSpecification")
	}
	return f.loadGenerationSpecification(generation)
}

func (f *fakeNodeReads) GenerationKept(nodeID string, generation uint64) (bool, error) {
	if f.generationKept == nil {
		unexpected(f.t, "GenerationKept")
	}
	return f.generationKept(nodeID, generation)
}

type fakeNodeTx struct {
	t                           testing.TB
	loadDeployment              func() (Record, error)
	loadNode                    func(string) (StoredNode, error)
	loadEnrollment              func(string) (EnrollmentRecord, error)
	loadGenerationSpecification func(uint64) (GenerationSpecification, error)
	generationKept              func(string, uint64) (bool, error)
	listNodes                   func() ([]NodeRecord, error)
	insertNode                  func(NewNode) (StoredNode, error)
	updateNode                  func(string, NodeLimits) error
	removeNode                  func(string) error
	createEnrollment            func(NewEnrollment) (time.Time, error)
	consumeEnrollment           func(string, string) (bool, error)
	heartbeatNode               func(Heartbeat) (bool, error)
	deleteGenerationStatus      func(string, uint64) error
	upsertGenerationStatus      func(GenerationStatusRecord) error
	promoteServingGeneration    func(string, uint64) error
	refreshServingReadiness     func(string, int) error
}

func (f *fakeNodeTx) LoadDeployment() (Record, error) {
	if f.loadDeployment == nil {
		unexpected(f.t, "LoadDeployment")
	}
	return f.loadDeployment()
}

func (f *fakeNodeTx) LoadNode(id string) (StoredNode, error) {
	if f.loadNode == nil {
		unexpected(f.t, "LoadNode")
	}
	return f.loadNode(id)
}

func (f *fakeNodeTx) LoadEnrollment(digest string) (EnrollmentRecord, error) {
	if f.loadEnrollment == nil {
		unexpected(f.t, "LoadEnrollment")
	}
	return f.loadEnrollment(digest)
}

func (f *fakeNodeTx) LoadGenerationSpecification(generation uint64) (GenerationSpecification, error) {
	if f.loadGenerationSpecification == nil {
		unexpected(f.t, "LoadGenerationSpecification")
	}
	return f.loadGenerationSpecification(generation)
}

func (f *fakeNodeTx) GenerationKept(nodeID string, generation uint64) (bool, error) {
	if f.generationKept == nil {
		unexpected(f.t, "GenerationKept")
	}
	return f.generationKept(nodeID, generation)
}

func (f *fakeNodeTx) ListNodes() ([]NodeRecord, error) {
	if f.listNodes == nil {
		unexpected(f.t, "ListNodes")
	}
	return f.listNodes()
}

func (f *fakeNodeTx) InsertNode(node NewNode) (StoredNode, error) {
	if f.insertNode == nil {
		unexpected(f.t, "InsertNode")
	}
	return f.insertNode(node)
}

func (f *fakeNodeTx) UpdateNode(id string, limits NodeLimits) error {
	if f.updateNode == nil {
		unexpected(f.t, "UpdateNode")
	}
	return f.updateNode(id, limits)
}

func (f *fakeNodeTx) RemoveNode(id string) error {
	if f.removeNode == nil {
		unexpected(f.t, "RemoveNode")
	}
	return f.removeNode(id)
}

func (f *fakeNodeTx) CreateEnrollment(enrollment NewEnrollment) (time.Time, error) {
	if f.createEnrollment == nil {
		unexpected(f.t, "CreateEnrollment")
	}
	return f.createEnrollment(enrollment)
}

func (f *fakeNodeTx) ConsumeEnrollment(digest, nodeID string) (bool, error) {
	if f.consumeEnrollment == nil {
		unexpected(f.t, "ConsumeEnrollment")
	}
	return f.consumeEnrollment(digest, nodeID)
}

func (f *fakeNodeTx) HeartbeatNode(heartbeat Heartbeat) (bool, error) {
	if f.heartbeatNode == nil {
		unexpected(f.t, "HeartbeatNode")
	}
	return f.heartbeatNode(heartbeat)
}

func (f *fakeNodeTx) DeleteGenerationStatus(nodeID string, generation uint64) error {
	if f.deleteGenerationStatus == nil {
		unexpected(f.t, "DeleteGenerationStatus")
	}
	return f.deleteGenerationStatus(nodeID, generation)
}

func (f *fakeNodeTx) UpsertGenerationStatus(status GenerationStatusRecord) error {
	if f.upsertGenerationStatus == nil {
		unexpected(f.t, "UpsertGenerationStatus")
	}
	return f.upsertGenerationStatus(status)
}

func (f *fakeNodeTx) PromoteServingGeneration(nodeID string, generation uint64) error {
	if f.promoteServingGeneration == nil {
		unexpected(f.t, "PromoteServingGeneration")
	}
	return f.promoteServingGeneration(nodeID, generation)
}

func (f *fakeNodeTx) RefreshServingReadiness(nodeID string, protocol int) error {
	if f.refreshServingReadiness == nil {
		unexpected(f.t, "RefreshServingReadiness")
	}
	return f.refreshServingReadiness(nodeID, protocol)
}

type fakeDeploymentTx struct {
	hasIncompatibleComputeState func(string) (bool, error)
	t                           testing.TB
	loadDeployment              func() (Record, error)
	loadSnapshot                func() (Snapshot, error)
	countResources              func() (Resources, error)
	claimInstallation           func(string) error
	setProcessDeployment        func(string, string, bool) error
	setManagerDeployment        func(string, string) error
	loadNode                    func(string) (StoredNode, error)
	insertNode                  func(NewNode) (StoredNode, error)
	updateNode                  func(string, NodeLimits) error
	saveSelection               func(SelectionRecord) error
	recordConfigurationMetadata func(json.RawMessage) error
	retainGeneration            func() error
	collectGenerations          func() error
	startReset                  func(string, int32, adminaudit.Source) error
	forceReset                  func() error
	cancelReset                 func() error
	loadResetSource             func() (adminaudit.Source, error)
	completeReset               func() error
	recordAudit                 func(string, string) error
	recordAuditAs               func(adminaudit.Source, string, string) error
}

func (f *fakeDeploymentTx) LoadDeployment() (Record, error) {
	if f.loadDeployment == nil {
		unexpected(f.t, "LoadDeployment")
	}
	return f.loadDeployment()
}

func (f *fakeDeploymentTx) LoadSnapshot() (Snapshot, error) {
	if f.loadSnapshot == nil {
		unexpected(f.t, "LoadSnapshot")
	}
	return f.loadSnapshot()
}

func (f *fakeDeploymentTx) CountResources() (Resources, error) {
	if f.countResources == nil {
		unexpected(f.t, "CountResources")
	}
	return f.countResources()
}

func (f *fakeDeploymentTx) ClaimInstallation(installationID string) error {
	if f.claimInstallation == nil {
		unexpected(f.t, "ClaimInstallation")
	}
	return f.claimInstallation(installationID)
}

func (f *fakeDeploymentTx) SetProcessDeployment(installationID, backendFingerprint string, admissionPaused bool) error {
	if f.setProcessDeployment == nil {
		unexpected(f.t, "SetProcessDeployment")
	}
	return f.setProcessDeployment(installationID, backendFingerprint, admissionPaused)
}

func (f *fakeDeploymentTx) SetManagerDeployment(provider, localNodeID string) error {
	if f.setManagerDeployment == nil {
		unexpected(f.t, "SetManagerDeployment")
	}
	return f.setManagerDeployment(provider, localNodeID)
}

func (f *fakeDeploymentTx) LoadNode(id string) (StoredNode, error) {
	if f.loadNode == nil {
		unexpected(f.t, "LoadNode")
	}
	return f.loadNode(id)
}

func (f *fakeDeploymentTx) InsertNode(node NewNode) (StoredNode, error) {
	if f.insertNode == nil {
		unexpected(f.t, "InsertNode")
	}
	return f.insertNode(node)
}

func (f *fakeDeploymentTx) UpdateNode(id string, limits NodeLimits) error {
	if f.updateNode == nil {
		unexpected(f.t, "UpdateNode")
	}
	return f.updateNode(id, limits)
}

func (f *fakeDeploymentTx) SaveSelection(selection SelectionRecord) error {
	if f.saveSelection == nil {
		unexpected(f.t, "SaveSelection")
	}
	return f.saveSelection(selection)
}

func (f *fakeDeploymentTx) RecordConfigurationMetadata(metadata json.RawMessage) error {
	if f.recordConfigurationMetadata == nil {
		unexpected(f.t, "RecordConfigurationMetadata")
	}
	return f.recordConfigurationMetadata(metadata)
}

func (f *fakeDeploymentTx) RetainGeneration() error {
	if f.retainGeneration == nil {
		unexpected(f.t, "RetainGeneration")
	}
	return f.retainGeneration()
}

func (f *fakeDeploymentTx) CollectGenerations() error {
	if f.collectGenerations == nil {
		unexpected(f.t, "CollectGenerations")
	}
	return f.collectGenerations()
}

func (f *fakeDeploymentTx) RecordAudit(action, installationID string) error {
	if f.recordAudit == nil {
		unexpected(f.t, "RecordAudit")
	}
	return f.recordAudit(action, installationID)
}

func (f *fakeDeploymentTx) StartReset(clear string, deadlineSeconds int32, source adminaudit.Source) error {
	if f.startReset == nil {
		unexpected(f.t, "StartReset")
	}
	return f.startReset(clear, deadlineSeconds, source)
}

func (f *fakeDeploymentTx) ForceReset() error {
	if f.forceReset == nil {
		unexpected(f.t, "ForceReset")
	}
	return f.forceReset()
}

func (f *fakeDeploymentTx) CancelReset() error {
	if f.cancelReset == nil {
		unexpected(f.t, "CancelReset")
	}
	return f.cancelReset()
}

func (f *fakeDeploymentTx) LoadResetSource() (adminaudit.Source, error) {
	if f.loadResetSource == nil {
		unexpected(f.t, "LoadResetSource")
	}
	return f.loadResetSource()
}

func (f *fakeDeploymentTx) CompleteReset() error {
	if f.completeReset == nil {
		unexpected(f.t, "CompleteReset")
	}
	return f.completeReset()
}

func (f *fakeDeploymentTx) RecordAuditAs(source adminaudit.Source, action, installationID string) error {
	if f.recordAuditAs == nil {
		unexpected(f.t, "RecordAuditAs")
	}
	return f.recordAuditAs(source, action, installationID)
}

func (f *fakeStorage) WithActivity(ctx context.Context, key AllocationKey, apply func(sessions.LockedSession, ActivityTx) error) error {
	if f.withActivity == nil {
		unexpected(f.t, "WithActivity")
	}
	return f.withActivity(ctx, key, apply)
}

func (f *fakeExecutionStorage) WithReservation(ctx context.Context, key AllocationKey, apply func(sessions.LockedSession, ReservationTx) error) error {
	if f.withReservation == nil {
		unexpected(f.t, "WithReservation")
	}
	return f.withReservation(ctx, key, apply)
}

func (f *fakeExecutionStorage) WithAllocation(ctx context.Context, key AllocationKey, apply func(AllocationTx) error) error {
	if f.withAllocation == nil {
		unexpected(f.t, "WithAllocation")
	}
	return f.withAllocation(ctx, key, apply)
}

func (f *fakeExecutionStorage) WithAllocationCleanup(ctx context.Context, key AllocationKey, apply func(AllocationCleanupTx) error) error {
	if f.withAllocationCleanup == nil {
		unexpected(f.t, "WithAllocationCleanup")
	}
	return f.withAllocationCleanup(ctx, key, apply)
}

func (f *fakeExecutionStorage) ClearWake(ctx context.Context, allocationID string, observed time.Time) error {
	if f.clearWake == nil {
		unexpected(f.t, "ClearWake")
	}
	return f.clearWake(ctx, allocationID, observed)
}

func (f *fakeReader) EnvironmentAllocation(ctx context.Context, key AllocationKey) (Allocation, error) {
	if f.environmentAllocation == nil {
		unexpected(f.t, "EnvironmentAllocation")
	}
	return f.environmentAllocation(ctx, key)
}

func (f *fakeReader) CredentialAllocations(ctx context.Context, after string) ([]Allocation, error) {
	if f.credentialAllocations == nil {
		unexpected(f.t, "CredentialAllocations")
	}
	return f.credentialAllocations(ctx, after)
}

func (f *fakeReader) ObservationSessions(ctx context.Context, after string, limit int) (ObservationSessionPage, error) {
	if f.observationSessions == nil {
		unexpected(f.t, "ObservationSessions")
	}
	return f.observationSessions(ctx, after, limit)
}

func (f *fakeReader) NodeAllocations(ctx context.Context, nodeID string) ([]NodeAllocation, error) {
	if f.nodeAllocations == nil {
		unexpected(f.t, "NodeAllocations")
	}
	return f.nodeAllocations(ctx, nodeID)
}

func (f *fakeReader) NodeOnline(ctx context.Context, nodeID string) (bool, error) {
	if f.nodeOnline == nil {
		unexpected(f.t, "NodeOnline")
	}
	return f.nodeOnline(ctx, nodeID)
}

func (f *fakeReader) LifecycleNodes(ctx context.Context) ([]string, error) {
	if f.lifecycleNodes == nil {
		unexpected(f.t, "LifecycleNodes")
	}
	return f.lifecycleNodes(ctx)
}

func (f *fakeReader) LifecycleAllocations(ctx context.Context, nodeID string, after string) ([]Allocation, error) {
	if f.lifecycleAllocations == nil {
		unexpected(f.t, "LifecycleAllocations")
	}
	return f.lifecycleAllocations(ctx, nodeID, after)
}

func (f *fakeReader) UnallocatedEnvironments(ctx context.Context, nodeID string, after string) ([]UnallocatedEnvironment, error) {
	if f.unallocatedEnvironments == nil {
		unexpected(f.t, "UnallocatedEnvironments")
	}
	return f.unallocatedEnvironments(ctx, nodeID, after)
}

func (f *fakeReader) LifecyclePlacement(ctx context.Context, key AllocationKey) (LifecyclePlacement, error) {
	if f.lifecyclePlacement == nil {
		unexpected(f.t, "LifecyclePlacement")
	}
	return f.lifecyclePlacement(ctx, key)
}

func (f *fakeReader) Activity(ctx context.Context, allocationID string) (Activity, error) {
	if f.activity == nil {
		unexpected(f.t, "Activity")
	}
	return f.activity(ctx, allocationID)
}

func (f *fakeReader) CountComputeReservations(ctx context.Context, installationID string) (int64, error) {
	if f.countComputeReservations == nil {
		unexpected(f.t, "CountComputeReservations")
	}
	return f.countComputeReservations(ctx, installationID)
}

func (f *fakeReader) CountRetainedAllocations(ctx context.Context, installationID string) (int64, error) {
	if f.countRetainedAllocations == nil {
		unexpected(f.t, "CountRetainedAllocations")
	}
	return f.countRetainedAllocations(ctx, installationID)
}

func (f *fakeDeploymentTx) HasIncompatibleComputeState(version string) (bool, error) {
	if f.hasIncompatibleComputeState == nil {
		f.t.Fatal("unexpected HasIncompatibleComputeState")
		return false, nil
	}
	return f.hasIncompatibleComputeState(version)
}
