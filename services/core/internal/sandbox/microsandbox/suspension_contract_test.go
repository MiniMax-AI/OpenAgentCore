package microsandbox

import (
	"context"
	"errors"
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
					return Response{Version: ProtocolVersion, State: &State{Compute: source, Status: "suspended", BootstrapComplete: true, Snapshot: &snap, SourceStopped: !reconcile}}, nil
				}
				if q.Operation != "kill" || q.Compute.ID != source.ID {
					t.Fatal("wrong source cleanup", q.Operation)
				}
				if cleanupFails {
					return Response{}, errors.New("lost cleanup")
				}
				return Response{Version: ProtocolVersion}, nil
			}))
			if e != nil {
				t.Fatal(e)
			}
			q := sandbox.SuspendRequest{Reference: r, OperationID: snap.OperationID, Source: compute(source), ReconcileOnly: reconcile}
			got, e := p.Suspend(deadline(t), q)
			if len(calls) != 2 || calls[0] != "suspend" || calls[1] != "kill" {
				t.Fatal(calls)
			}
			if cleanupFails {
				if e == nil || got.SuspendSettled {
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
