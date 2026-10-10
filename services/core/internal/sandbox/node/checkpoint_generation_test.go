package node

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestStaticNodeReportsExactCheckpointGeneration(t *testing.T) {
	id := identity()
	compatibility := &sandbox.CheckpointCompatibility{ArtifactDomain: "private-store", ExecutionClass: "native-class"}
	status := sandbox.GenerationStatus{Generation: id.DeploymentGeneration, SpecificationDigest: id.SpecificationDigest, State: "ready", Checkpoint: compatibility}
	seen := false
	hub := NewHub(HubOptions{Heartbeat: func(_ context.Context, _ Identity, _ string, _ uint64, health Health) error {
		seen = true
		if len(health.Generations) != 1 || *health.Generations[0].Checkpoint != *compatibility {
			t.Fatal("qualification lost")
		}
		return nil
	}})
	p := &peer{identity: id}
	health := Health{ProviderReady: true, Generations: []sandbox.GenerationStatus{status}}
	if err := hub.recordHealth(t.Context(), p, health); err != nil || !seen {
		t.Fatal(err, seen)
	}
	for _, mutation := range []string{"generation", "digest", "readiness", "extra"} {
		t.Run(mutation, func(t *testing.T) {
			changed := health
			changed.Generations = append([]sandbox.GenerationStatus(nil), health.Generations...)
			switch mutation {
			case "generation":
				changed.Generations[0].Generation++
			case "digest":
				changed.Generations[0].SpecificationDigest = strings.Repeat("f", 64)
			case "readiness":
				changed.ProviderReady = false
			case "extra":
				changed.Generations = append(changed.Generations, status)
			}
			seen = false
			if err := hub.recordHealth(t.Context(), p, changed); err == nil || seen {
				t.Fatal("invalid static qualification reached persistence")
			}
		})
	}
}

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
	hub := NewHub(HubOptions{Authenticate: func(_ context.Context, node, token string) (Identity, error) {
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
		done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: directory, Identity: id, Credential: credential, Provider: &closedRestoreProvider{}, Probe: probe})
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
