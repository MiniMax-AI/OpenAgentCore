package sandbox

import (
	"encoding/json"
	"testing"
)

func TestRetainedCompatibilityIsOptionalButNeverPartial(t *testing.T) {
	legacy := `{"Reference":"native","ID":"one","Data":"opaque","OperationID":"capture","SourceGeneration":1,"SourceName":"source","SourceID":"one"}`
	var retained RetainedState
	if err := json.Unmarshal([]byte(legacy), &retained); err != nil || ValidateRetained(retained) != nil {
		t.Fatal("legacy native pause envelope rejected", err)
	}
	for _, compatibility := range []CheckpointCompatibility{{}, {ArtifactDomain: "store"}, {ExecutionClass: "class"}, {ArtifactDomain: "store", ExecutionClass: "class"}} {
		retained.Compatibility = compatibility
		wantValid := compatibility == (CheckpointCompatibility{}) || compatibility.Validate() == nil
		if (ValidateRetained(retained) == nil) != wantValid {
			t.Fatal("partial compatibility accepted", compatibility)
		}
	}
}

func TestRetainedClosureCannotAuthorizeAnotherRestore(t *testing.T) {
	retained := RetainedState{Reference: "native", ID: "one", Data: "opaque", OperationID: "capture", SourceGeneration: 1, SourceName: "source", SourceID: "one"}
	target := Compute{Generation: 2, Name: "target", RestoredFrom: &retained}
	q := ResumeRequest{OperationID: "restore", Retained: retained, Target: target, ReconcileOnly: true}
	good := ComputeState{Compute: target, Status: "absent", RestoreAttemptClosed: "restore"}
	if !good.ClosesRestoreAttempt(q) {
		t.Fatal("exact settled closure rejected")
	}
	for _, kind := range []string{"operation", "generation", "provenance", "dispatched", "fresh"} {
		s, request := good, q
		switch kind {
		case "operation":
			s.RestoreAttemptClosed = "other"
		case "generation":
			s.Compute.Generation++
		case "provenance":
			other := retained
			other.Data = "foreign"
			s.Compute.RestoredFrom = &other
		case "dispatched":
			s.Compute.ID = "native-target"
		case "fresh":
			request.ReconcileOnly = false
		}
		if s.ClosesRestoreAttempt(request) {
			t.Fatal("unbound closure accepted", kind)
		}
	}
}
