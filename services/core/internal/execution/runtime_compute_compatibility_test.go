package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
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

func TestRestoreUsesAllocationGenerationWorkspaceAfterDeploymentChange(t *testing.T) {
	owner := deployment.Allocation{ID: "allocation", ProviderKey: "installation", DeploymentGeneration: 4}
	for _, originalExternal := range []bool{false, true} {
		t.Run(fmt.Sprint(originalExternal), func(t *testing.T) {
			original, current := sandbox.DeploymentSpec{}, sandbox.DeploymentSpec{Workspace: &workspacefs.Declaration{}}
			if originalExternal {
				original, current = current, original
			}
			prior, _ := json.Marshal(original)
			latest, _ := json.Marshal(current)
			r := runtimeLifecycle{config: RuntimeProvider{Workspace: current.Workspace}, reader: &strictDeploymentReader{t: t, allocation: func(context.Context, sandbox.Reference) (deployment.AllocationRecord, error) {
				return deployment.AllocationRecord{ID: owner.ID, InstallationID: owner.ProviderKey, Generation: 4, Deployment: deployment.Record{Generation: 5, Specification: latest}, Retained: &deployment.GenerationRecord{Generation: 4, Specification: prior}}, nil
			}}}
			binding, err := r.restoreWorkspace(t.Context(), owner)
			if originalExternal {
				if !errors.Is(err, workspacefs.ErrUnavailable) {
					t.Fatal("original external storage was bypassed", err)
				}
			} else if err != nil || binding != nil {
				t.Fatal("old owned disk depended on new workspace", err)
			}
		})
	}
}
