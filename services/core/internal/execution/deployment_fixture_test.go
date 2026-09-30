package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

// fixturePublicURL is the installation public URL of these tests. E2B requires
// a public origin, so it is never loopback.
const fixturePublicURL = "https://core.example"

// testDeployment builds the pooled deployment service and the deployment
// execution operations on lease, as cmd/server does for the Worker. cipher is
// nil when the owner has no credential key.
func testDeployment(t *testing.T, pool *pgxpool.Pool, cipher *credentialcrypto.Cipher, lease *pgunit.Lease) (*deployment.Service, *deployment.ExecutionOperations) {
	t.Helper()
	adapter := deploymentpg.New(pgunit.NewPool(pool), cipher)
	return deploymentOperations(t, adapter, adapter, deploymentpg.NewExecution(lease, cipher))
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
	service, err := deployment.NewService(storage, reader, providers.Builtin(), fixturePublicURL)
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
	t              *testing.T
	withDeployment func(context.Context, func(deployment.DeploymentTx) error) error
}

func (s *strictExecutionStorage) WithDeployment(ctx context.Context, apply func(deployment.DeploymentTx) error) error {
	if s.withDeployment == nil {
		return unexpectedDeploymentCall(s.t, "WithDeployment")
	}
	return s.withDeployment(ctx, apply)
}

// strictDeploymentReader runs each set func; any other call fails the test.
type strictDeploymentReader struct {
	t           *testing.T
	deployment  func(context.Context) (deployment.Record, error)
	snapshot    func(context.Context) (deployment.Snapshot, error)
	ownerEpoch  func(context.Context) (uint64, error)
	allocation  func(context.Context, sandbox.Reference) (deployment.AllocationRecord, error)
	generations func(context.Context, int64) ([]deployment.GenerationRecord, error)
	nodes       func(context.Context) ([]deployment.NodeRecord, error)
	nodeHistory func(context.Context, string, coremetrics.Range) (deployment.NodeRecord, []deployment.HostHistoryPoint, error)
	readNodes   func(context.Context, func(deployment.NodeReads) error) error
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
