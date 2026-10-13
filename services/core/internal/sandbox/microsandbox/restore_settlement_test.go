package microsandbox

import (
	"testing"
)

func TestRestoreClosedReceiptRequiresExactObserveOnlyOperation(t *testing.T) {
	p := &Provider{config: testConfig()}
	snapshot := testSnapshot()
	target := Compute{Name: Name(testConfig(), testRef(), 1), Generation: 1, RestoredFrom: &snapshot}
	q := Request{Operation: "resume", Reference: testRef(), Resume: &ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", Target: target, Snapshot: snapshot, ObserveOnly: true}}
	valid := State{Compute: target, Status: "absent", RestoreAttemptClosed: q.Resume.OperationID}
	for _, kind := range []string{"exact", "wrong operation", "native ID", "foreign target", "bootstrap", "snapshot", "source stopped", "not observation"} {
		t.Run(kind, func(t *testing.T) {
			request := q
			resume := *q.Resume
			request.Resume = &resume
			state := valid
			switch kind {
			case "wrong operation":
				state.RestoreAttemptClosed = "wrong"
			case "native ID":
				state.Compute.ID = "local:1"
			case "foreign target":
				state.Compute.Name = "foreign"
			case "bootstrap":
				state.BootstrapComplete = true
			case "snapshot":
				state.Snapshot = &snapshot
			case "source stopped":
				state.SourceStopped = true
			case "not observation":
				request.Resume.ObserveOnly = false
			}
			_, err := p.responseState(t.Context(), request, Response{State: &state})
			if (kind == "exact") != (err == nil) {
				t.Fatal(kind, err)
			}
		})
	}
}
