package node

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestGenerationWireRoutesOldOwnershipAndCurrentTargetSeparately(t *testing.T) {
	id := identity()
	id.DeploymentGeneration = 1
	id.SpecificationDigest = strings.Repeat("a", 64)
	manager := generationFixture(17)
	original := manager.values[1].value.Provider.(*fakeProvider)
	current := manager.values[17].value.Provider.(*fakeProvider)
	ninth := manager.values[9].value.Provider.(*fakeProvider)
	hub := NewHub(HubOptions{
		Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil },
		OwnerEpoch:   func(context.Context) (uint64, error) { return 7, nil },
		Generations: func(_ context.Context, _ Identity, _ string, _ uint64, h Health) error {
			if len(h.Generations) > 8 {
				t.Error("unbounded wire observations")
			}
			return nil
		},
		Retention: func(_ context.Context, _ Identity, _ string, _ uint64, refs []sandbox.GenerationReference) (sandbox.NodeDeployment, []sandbox.GenerationRetention, error) {
			result := make([]sandbox.GenerationRetention, 0, len(refs))
			for _, ref := range refs {
				result = append(result, sandbox.GenerationRetention{GenerationReference: ref, Keep: true})
			}
			return sandbox.NodeDeployment{Generation: 17, SpecificationDigest: id.SpecificationDigest, ServingGeneration: &id.DeploymentGeneration}, result, nil
		},
	})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: stored.Credential, Generations: manager})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("generation node did not stop")
		}
	}()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	for _, generation := range []uint64{1, 17, 9} {
		proxy := hub.GenerationProvider("docker", docker.Operations(), func(context.Context, sandbox.Reference) (string, uint64, error) { return id.NodeID, generation, nil })
		ref := reference()
		if _, err := proxy.GetInfo(ctx, ref); err != nil {
			t.Fatal("retained generation info failed", generation, err)
		}
		if generation != 9 {
			if _, err := proxy.Create(ctx, sandbox.Bootstrap{Reference: ref}); err != nil {
				t.Fatal("exact ready provider Create failed", generation, err)
			}
		}
	}
	for generation, p := range map[int]*fakeProvider{1: original, 17: current, 9: ninth} {
		p.mu.Lock()
		reads, creates := p.reads, p.creates
		p.mu.Unlock()
		want := 1
		if generation == 9 {
			want = 0
		}
		if reads != 1 || creates != want {
			t.Fatal("generation request substituted another provider", generation, reads, creates)
		}
	}
}
