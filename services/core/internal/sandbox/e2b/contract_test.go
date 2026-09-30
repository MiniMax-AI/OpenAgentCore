package e2b

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
	"github.com/google/uuid"
	"testing"
)

type contractCaller func(context.Context, Request) (Response, error)

func (f contractCaller) Call(ctx context.Context, q Request) (Response, error) { return f(ctx, q) }

func TestProviderContract(t *testing.T) {
	contracttest.RunFailures(t, func(t *testing.T, s contracttest.Scenario, cancel context.CancelFunc) contracttest.Fixture {
		p, _, r := fixture(t)
		b := sandbox.Bootstrap{Reference: r, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "synthetic", NetworkAccess: "enabled"}
		var calls []string
		p.caller = contractCaller(func(ctx context.Context, q Request) (Response, error) {
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
				return Response{Version: ProtocolVersion, Info: &sandbox.Info{Reference: foreign, ProviderID: "foreign", State: "running", CreateSettled: true, BootstrapComplete: true}}, nil
			case contracttest.CleanupFailure:
				return Response{Version: ProtocolVersion, ErrorCode: "unconfirmed"}, nil
			default:
				return Response{}, errors.New("lost helper response")
			}
		})
		return contracttest.Fixture{Provider: p, Bootstrap: b, Calls: func() []string { return calls }, WantCalls: []string{s.Operation}}
	})
}

func TestProviderContractObservation(t *testing.T) {
	p, f, r := fixture(t)
	f.response.Info = &sandbox.Info{Reference: r, ProviderID: "native-owned", State: "running", CreateSettled: true, BootstrapComplete: true}
	b := sandbox.Bootstrap{Reference: r, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "synthetic", NetworkAccess: "enabled"}
	got, err := p.Create(bounded(t), b)
	contracttest.AssertObservation(t, got, err, r, "native-owned", "running")
	got, err = p.GetInfo(bounded(t), r)
	contracttest.AssertObservation(t, got, err, r, "native-owned", "running")
	got, err = p.Renew(bounded(t), r)
	contracttest.AssertObservation(t, got, err, r, "native-owned", "running")
}
