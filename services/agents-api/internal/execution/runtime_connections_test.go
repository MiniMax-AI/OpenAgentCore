package execution

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestRuntimeCleanupDropsOnlyOwnedEnvironmentConnection(t *testing.T) {
	first := store.RuntimeAllocation{ID: "allocation-one", EnvironmentID: "environment-one"}
	second := store.RuntimeAllocation{ID: "allocation-two", EnvironmentID: "environment-two"}
	retained := &runtimeConnection{}
	r := &runtimeLifecycle{
		connections: map[string]*runtimeConnection{first.EnvironmentID: {}, second.EnvironmentID: retained},
	}
	r.clearRuntimeState(first)
	if len(r.connections) != 1 || r.connections[second.EnvironmentID] != retained {
		t.Fatal("cleanup retained the retired connection or discarded another Runtime's state")
	}
	r.clearRuntimeState(second)
	if len(r.connections) != 0 {
		t.Fatal("cleanup retained owned connection or initialization")
	}
}
