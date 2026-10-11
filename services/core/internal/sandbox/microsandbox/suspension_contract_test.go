package microsandbox

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"testing"
)

func TestSharedSuspendSettlesExactSourceCleanupInsideAdapter(t *testing.T) {
	for _, reconcile := range []bool{false, true} {
		for _, cleanupFails := range []bool{false, true} {
			c, r, snap := testConfig(), testRef(), testSnapshot()
			source := Compute{Name: snap.SourceName, ID: snap.SourceID}
			calls := []string{}
			p, e := NewWithCaller(c, callerFunc(func(_ context.Context, q Request) (Response, error) {
				calls = append(calls, q.Operation)
				if q.Operation == "suspend" {
					if q.Suspend.ObserveOnly != reconcile {
						t.Fatal("reconcile intent changed")
					}
					return Response{Version: ProtocolVersion, State: &State{Compute: source, Status: "suspended", BootstrapComplete: true, Snapshot: &snap, SourceStopped: !cleanupFails}}, nil
				}
				t.Fatal("unexpected extra native operation", q.Operation)
				return Response{}, nil
			}))
			if e != nil {
				t.Fatal(e)
			}
			q := sandbox.SuspendRequest{Reference: r, OperationID: snap.OperationID, Source: compute(source), ReconcileOnly: reconcile}
			got, e := p.Suspend(deadline(t), q)
			if len(calls) != 1 || calls[0] != "suspend" {
				t.Fatal(calls)
			}
			if cleanupFails {
				if got.ResourcesReleased || (e == nil && sandbox.ValidateSuspendResult(q, got) == nil) {
					t.Fatal("failed cleanup released capacity")
				}
				continue
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = sandbox.ValidateSuspendResult(q, got); e != nil {
				t.Fatal(e)
			}
			if got.Retained == nil || got.Retained.Data == "" {
				t.Fatal("missing native proof")
			}
		}
	}
}
