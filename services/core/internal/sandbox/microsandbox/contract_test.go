package microsandbox

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
	"github.com/google/uuid"
	"testing"
)

func TestProviderContract(t *testing.T) {
	contracttest.RunFailures(t, func(t *testing.T, s contracttest.Scenario, cancel context.CancelFunc) contracttest.Fixture {
		c, r := testConfig(), testRef()
		b := sandbox.Bootstrap{Reference: r, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "synthetic", NetworkAccess: "enabled"}
		var calls []string
		p, err := NewWithCaller(c, callerFunc(func(ctx context.Context, q Request) (Response, error) {
			calls = append(calls, q.Operation)
			if q.Reference != r {
				t.Fatal("native request lost allocation identity")
			}
			switch s.Fault {
			case contracttest.Canceled:
				cancel()
				return Response{}, ctx.Err()
			case contracttest.ForeignOwnership:
				if s.Operation == "kill" {
					return Response{Version: ProtocolVersion, ErrorCode: "ownership"}, nil
				}
				foreign := r
				foreign.AllocationID = uuid.NewString()
				return Response{Version: ProtocolVersion, State: &State{Compute: Compute{Name: Name(c, foreign, 0), ID: "foreign"}, Status: "running", BootstrapComplete: true}}, nil
			case contracttest.CleanupFailure:
				return Response{Version: ProtocolVersion, ErrorCode: "unconfirmed"}, nil
			default:
				return Response{}, errors.New("lost helper response")
			}
		}))
		if err != nil {
			t.Fatal(err)
		}
		nativeOperation := s.Operation
		if nativeOperation == "renew" {
			nativeOperation = "inspect"
		}
		return contracttest.Fixture{Provider: p, Bootstrap: b, Calls: func() []string { return calls }, WantCalls: []string{nativeOperation}}
	})
}

func TestProviderContractObservation(t *testing.T) {
	c, r := testConfig(), testRef()
	p, err := NewWithCaller(c, callerFunc(func(_ context.Context, q Request) (Response, error) {
		if q.Operation != "inspect" && q.Operation != "create" {
			t.Fatal("observation mutated compute")
		}
		status := "stopped"
		if q.Operation == "create" {
			status = "running"
		}
		return Response{Version: ProtocolVersion, State: &State{Compute: Compute{Name: Name(c, r, 0), ID: "native-owned"}, Status: status}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	b := sandbox.Bootstrap{Reference: r, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "synthetic", NetworkAccess: "enabled"}
	got, err := p.Create(deadline(t), b)
	contracttest.AssertObservation(t, got, err, r, "native-owned", "running")
	got, err = p.GetInfo(deadline(t), r)
	contracttest.AssertObservation(t, got, err, r, "native-owned", "stopped")
	got, err = p.Renew(deadline(t), r)
	contracttest.AssertObservation(t, got, err, r, "native-owned", "stopped")
}
