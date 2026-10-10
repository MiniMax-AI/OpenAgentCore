package sandbox

import (
	"strings"
	"testing"
)

func TestSuspensionRequiresBoundSettledResourceRelease(t *testing.T) {
	c := Compute{ID: "native", Name: "source", Generation: 2}
	r := RetainedState{Reference: "allocation", ID: "retained", OperationID: "op", SourceGeneration: 2, SourceName: "source", SourceID: "native", Data: "private-proof"}
	q := SuspendRequest{OperationID: "op", Source: c}
	valid := ComputeState{Compute: c, Status: "suspended", BootstrapComplete: true, Retained: &r, ResourcesReleased: true, SuspendSettled: true}
	if err := ValidateSuspendResult(q, valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*ComputeState)
	}{
		{"unsettled", func(s *ComputeState) { s.SuspendSettled = false }},
		{"capacity still held", func(s *ComputeState) { s.ResourcesReleased = false }},
		{"running", func(s *ComputeState) { s.Status = "running" }},
		{"foreign source", func(s *ComputeState) { s.Compute.ID = "foreign" }},
		{"foreign handle", func(s *ComputeState) { v := *s.Retained; v.OperationID = "other"; s.Retained = &v }},
		{"oversized", func(s *ComputeState) { v := *s.Retained; v.Data = strings.Repeat("x", 65537); s.Retained = &v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := valid
			tc.change(&s)
			if ValidateSuspendResult(q, s) == nil {
				t.Fatal("invalid suspension accepted")
			}
		})
	}
	rollback := ComputeState{Compute: c, Status: "running", BootstrapComplete: true, SuspendSettled: true}
	if ValidateSuspendResult(q, rollback) == nil {
		t.Fatal("fresh dispatch accepted rollback")
	}
	q.ReconcileOnly = true
	if err := ValidateSuspendResult(q, rollback); err != nil {
		t.Fatal(err)
	}
	rollback.SuspendSettled = false
	if ValidateSuspendResult(q, rollback) == nil {
		t.Fatal("running observation inferred settlement")
	}
}
func TestComputeResultFencesSameNativeIDAcrossGenerations(t *testing.T) {
	old := Compute{ID: "same-native", Name: "allocation", Generation: 1}
	next := old
	next.Generation++
	if ValidateComputeResult(old, next) == nil {
		t.Fatal("stale logical generation accepted")
	}
}
