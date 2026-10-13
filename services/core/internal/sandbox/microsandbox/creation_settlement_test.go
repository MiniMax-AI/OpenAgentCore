package microsandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestCreateConfigurationRejectionSettlement(t *testing.T) {
	config, ref := testConfig(), testRef()
	bootstrap := sandbox.Bootstrap{Reference: ref, SessionID: ref.TenantID, DeviceID: ref.EnvironmentID,
		CoreURL: "https://core.example/api/v1", Credential: "fixture", NetworkAccess: "disabled"}
	for _, test := range []struct {
		name    string
		change  func(*Response)
		failure error
		settled bool
	}{
		{name: "exact initial compute", settled: true},
		{name: "no proof", change: func(r *Response) { r.CreateSettled = false }},
		{name: "no state", change: func(r *Response) { r.State = nil }},
		{name: "no native identity", change: func(r *Response) { r.State.Compute.ID = "" }},
		{name: "foreign allocation", change: func(r *Response) { r.State.Compute.Name = "foreign" }},
		{name: "foreign generation", change: func(r *Response) { r.State.Compute.Generation = 1 }},
		{name: "restored compute", change: func(r *Response) { value := testSnapshot(); r.State.Compute.RestoredFrom = &value }},
		{name: "bootstrap already complete", change: func(r *Response) { r.State.BootstrapComplete = true }},
		{name: "absence is not proof", change: func(r *Response) { r.State.Status = "absent" }},
		{name: "missing status", change: func(r *Response) { r.State.Status = "" }},
		{name: "wrong protocol", change: func(r *Response) { r.Version++ }},
		{name: "ownership rejection", change: func(r *Response) { r.ErrorCode = "ownership" }},
		{name: "unknown create", change: func(r *Response) { r.ErrorCode = "unconfirmed" }},
		{name: "lost response", failure: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := Response{Version: ProtocolVersion, CreateSettled: true, ErrorCode: "invalid",
				State: &State{Compute: Compute{Name: Name(config, ref, 0), ID: "local:created"}, Status: "running"}}
			if test.change != nil {
				test.change(&response)
			}
			calls := 0
			provider, err := NewWithCaller(config, callerFunc(func(_ context.Context, request Request) (Response, error) {
				calls++
				if request.Operation != "create" || request.Reference != ref {
					t.Fatal("changed creation request")
				}
				return response, test.failure
			}))
			if err != nil {
				t.Fatal(err)
			}
			info, err := provider.Create(deadline(t), bootstrap)
			if err == nil || info.CreateSettled != test.settled || info.BootstrapComplete || calls != 1 {
				t.Fatal("rejection changed readiness or settlement", info, err, calls)
			}
			if test.settled && (!errors.Is(err, sandbox.ErrInvalid) || info.Reference != ref || info.ProviderID != "local:created" || info.State == "absent") {
				t.Fatal("settlement lost the original rejection or compute identity", info, err)
			}
		})
	}
}

func TestExistingComputeObservationCannotSettleCreation(t *testing.T) {
	for _, code := range []string{"", "invalid", "ownership", "unconfirmed"} {
		t.Run(code, func(t *testing.T) {
			config, ref := testConfig(), testRef()
			provider, err := NewWithCaller(config, callerFunc(func(_ context.Context, q Request) (Response, error) {
				return Response{Version: ProtocolVersion, ErrorCode: code, CreateSettled: true,
					State: &State{Compute: Compute{Name: Name(config, ref, 0), ID: "local:observed"}, Status: "running"}}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			for _, read := range []func(context.Context, sandbox.Reference) (sandbox.Info, error){provider.GetInfo, provider.Renew} {
				info, err := read(deadline(t), ref)
				if (err == nil) != (code == "") || info.CreateSettled {
					t.Fatal("ordinary inspection acquired creation settlement", info, err)
				}
			}
		})
	}
}

func TestInitialInfoAcceptsOnlyExactSettledAbsence(t *testing.T) {
	for _, name := range []string{"exact", "bare not found", "no proof", "no state", "wrong version", "native ID", "foreign name", "generation", "restore", "bootstrap", "snapshot", "stopped source", "transport error", "unknown"} {
		t.Run(name, func(t *testing.T) {
			config, ref := testConfig(), testRef()
			out := Response{Version: ProtocolVersion, CreateSettled: true, State: &State{Compute: Compute{Name: Name(config, ref, 0)}, Status: "absent"}}
			var transportErr error
			switch name {
			case "bare not found":
				out.ErrorCode = "not_found"
			case "no proof":
				out.CreateSettled = false
			case "no state":
				out.State = nil
			case "wrong version":
				out.Version--
			case "native ID":
				out.State.Compute.ID = "native:id"
			case "foreign name":
				out.State.Compute.Name = "foreign"
			case "generation":
				out.State.Compute.Generation = 1
			case "restore":
				out.State.Compute.RestoredFrom = &SnapshotIdentity{}
			case "bootstrap":
				out.State.BootstrapComplete = true
			case "snapshot":
				out.State.Snapshot = &SnapshotIdentity{}
			case "stopped source":
				out.State.SourceStopped = true
			case "transport error":
				transportErr = context.DeadlineExceeded
			case "unknown":
				out.ErrorCode = "unconfirmed"
			}
			provider, err := NewWithCaller(config, callerFunc(func(_ context.Context, q Request) (Response, error) {
				if q.Operation != "initial_info" || q.Compute != (Compute{Name: Name(config, ref, 0)}) {
					t.Fatal("ambiguous initial observation", q.Operation, q.Compute)
				}
				return out, transportErr
			}))
			if err != nil {
				t.Fatal(err)
			}
			for _, read := range []func(context.Context, sandbox.Reference) (sandbox.Info, error){provider.GetInfo, provider.Renew} {
				info, err := read(deadline(t), ref)
				if name == "exact" {
					if err != nil || info != (sandbox.Info{Reference: ref, State: "absent", CreateSettled: true}) {
						t.Fatal("lost absence receipt", info, err)
					}
				} else if info.CreateSettled {
					t.Fatal("invalid absence settled", info, err)
				}
			}
		})
	}
}

func TestOrdinaryComputeInspectionDoesNotRequestInitialSettlement(t *testing.T) {
	config, ref := testConfig(), testRef()
	provider, err := NewWithCaller(config, callerFunc(func(_ context.Context, q Request) (Response, error) {
		if q.Operation != "inspect" {
			t.Fatal("ordinary compute changed admission", q.Operation)
		}
		return Response{Version: ProtocolVersion, ErrorCode: "not_found"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.GetCompute(deadline(t), ref, Compute{Name: Name(config, ref, 0)})
	if !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatal(err)
	}
}
