package execution

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"reflect"
	"testing"
)

// This literal freezes the deployed beta v1 shape independently of current types.
func TestBetaRetainedV1RoundTripPreservesEveryField(t *testing.T) {
	const raw = `{"protocol_version":"1","current":{"Generation":4,"Name":"owned-source","ID":"native-source","RestoredFrom":{"Reference":"owned/reference","ID":"native-parent","Data":"{\"version\":1,\"sandbox_id\":\"native-source\"}","OperationID":"prior-operation","SourceGeneration":3,"SourceName":"prior-source","SourceID":"native-parent"}},"target":{"Generation":5,"Name":"owned-target","ID":"","RestoredFrom":{"Reference":"owned/reference","ID":"native-source","Data":"{\"version\":1,\"sandbox_id\":\"native-source\"}","OperationID":"pause-operation","SourceGeneration":4,"SourceName":"owned-source","SourceID":"native-source"}},"retained":{"Reference":"owned/reference","ID":"native-source","Data":"{\"version\":1,\"sandbox_id\":\"native-source\"}","OperationID":"pause-operation","SourceGeneration":4,"SourceName":"owned-source","SourceID":"native-source"},"suspend_id":"pause-operation","restore_id":"resume-operation","rollback":true}`
	var state runtimeCompute
	if err := decodeRuntimeCompute([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if json.Unmarshal([]byte(raw), &before) != nil || json.Unmarshal(out, &after) != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("beta v1 field loss or rewrite")
	}
	for _, invalid := range []string{`{}`, `{"protocol_version":"2"}`, `{"protocol_version":"1","snapshot":{}}`, `{"protocol_version":"1","current":{"RestoredFrom":{"Digest":"legacy"}}}`, `{"protocol_version":"1","target":{"RestoredFrom":{"CheckpointRoot":"legacy"}}}`, raw + `{}`} {
		if decodeRuntimeCompute([]byte(invalid), new(runtimeCompute)) == nil {
			t.Fatal("incompatible receipt accepted")
		}
	}
}

func TestReleasedDirectAllocationStillChecksCapacity(t *testing.T) {
	for _, limit := range []string{"active", "retained"} {
		t.Run(limit, func(t *testing.T) {
			r := runtimeLifecycle{sessions: workspaceEnvironmentReader{}, config: RuntimeProvider{Mode: "direct", InstallationID: "fixture", Generation: 1, Suspension: &RuntimeSuspensionPolicy{MaxActive: 1, MaxRetained: 2}}, reader: &strictDeploymentReader{t: t,
				environmentAllocation: func(context.Context, deployment.AllocationKey) (deployment.Allocation, error) {
					return deployment.Allocation{State: "released"}, nil
				},
				countComputeReservations: func(context.Context, string) (int64, error) {
					if limit == "active" {
						return 1, nil
					}
					return 0, nil
				},
				countRetainedAllocations: func(context.Context, string) (int64, error) { return 2, nil },
			}}
			if _, err := r.provision(t.Context(), "tenant", "environment", "fixture"); !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatal("released owner bypassed direct capacity", err)
			}
		})
	}
}
