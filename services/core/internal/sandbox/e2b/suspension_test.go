package e2b

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestSuspensionPlansSameNativeIDWithoutCallingHelper(t *testing.T) {
	p, f, r := fixture(t)
	initial, err := p.Initial(bounded(t), r)
	if err != nil || initial.Name != r.AllocationID || initial.ID != "" {
		t.Fatal(initial, err)
	}
	retained := sandbox.RetainedState{Reference: r.AllocationID, ID: uuid.NewString(), OperationID: uuid.NewString(), SourceName: r.AllocationID, SourceID: "native-id", Data: "opaque"}
	target, err := p.NewCompute(bounded(t), r, 1, &retained)
	if err != nil || target.ID != retained.SourceID || target.Generation != 1 || *target.RestoredFrom != retained || len(f.requests) != 0 {
		t.Fatal(target, err)
	}
	if _, err = p.NewCompute(bounded(t), r, 2, &retained); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("generation jump accepted", err)
	}
	retained.Reference = uuid.NewString()
	if _, err = p.NewCompute(bounded(t), r, 1, &retained); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("foreign allocation accepted", err)
	}
}
func TestSuspensionRejectsUnsettledAndForeignHelperOutcomes(t *testing.T) {
	p, f, r := fixture(t)
	current := sandbox.Compute{Name: r.AllocationID, ID: "native-id"}
	q := sandbox.SuspendRequest{Reference: r, Source: current, OperationID: uuid.NewString()}
	retained := sandbox.RetainedState{Reference: r.AllocationID, ID: q.OperationID, OperationID: q.OperationID, SourceName: current.Name, SourceID: current.ID, Data: "opaque"}
	f.response.State = &sandbox.ComputeState{Compute: current, Status: "suspended", BootstrapComplete: true, Retained: &retained, ResourcesReleased: true, SuspendSettled: true}
	if _, err := p.Suspend(bounded(t), q); err != nil {
		t.Fatal(err)
	}
	f.response.State.SuspendSettled = false
	if _, err := p.Suspend(bounded(t), q); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatal(err)
	}
	f.response.State.SuspendSettled = true
	f.response.State.Compute.Generation = 1
	if _, err := p.Suspend(bounded(t), q); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal(err)
	}
}
