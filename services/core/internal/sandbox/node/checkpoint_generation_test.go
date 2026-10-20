package node

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"github.com/google/uuid"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

type closedRestoreProvider struct{ fakeProvider }

func (*closedRestoreProvider) ProviderOperations() providercontract.Operations {
	return microsandbox.Operations()
}
func (*closedRestoreProvider) Resume(_ context.Context, q sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{Compute: q.Target, Status: "absent", RestoreAttemptClosed: q.OperationID}, nil
}
func TestClosedRestoreEvidenceSurvivesNodeTransport(t *testing.T) {
	id := identity()
	var credential string
	hub := newTestHub(HubOptions{Authenticate: func(_ context.Context, node, token string) (Identity, error) {
		if node != id.NodeID || token != credential {
			return Identity{}, ErrAuthentication
		}
		return id, nil
	}, OwnerEpoch: func(context.Context) (uint64, error) { return 7, nil }})
	defer hub.Close()
	server := httptest.NewServer(hub)
	defer server.Close()
	directory := stateDir(t)
	stored, err := InitIdentity(directory, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	credential = stored.Credential
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runProvider(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: directory, Identity: id, Credential: credential}, &closedRestoreProvider{}, probe)
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	snapshot := sandbox.SnapshotIdentity{ID: "snapshot", Compatibility: sandbox.CheckpointCompatibility{ArtifactDomain: "store", ExecutionClass: "class"}}
	q := sandbox.ResumeRequest{Reference: reference(), OperationID: uuid.NewString(), Snapshot: snapshot, Target: sandbox.Compute{Generation: 1, Name: "exact-target", RestoredFrom: &snapshot}, ObserveOnly: true}
	result, err := hub.Proxy(id.NodeID, microsandbox.Operations(), id.DeploymentGeneration).Resume(t.Context(), q)
	if err != nil || !result.ClosesRestoreAttempt(q) {
		t.Fatalf("exact closed proof lost in proxy: %#v %v", result, err)
	}
}
