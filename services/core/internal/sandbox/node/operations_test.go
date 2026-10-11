package node

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/google/uuid"
)

func TestUnsupportedWireIsExplicitAndDoesNotInvokeProvider(t *testing.T) {
	p := &fakeProvider{}
	q := request{ID: uuid.NewString(), ConnectionID: uuid.NewString(), Reference: reference(), TimeoutMillis: 1000, Operation: "initial"}
	out := execute(t.Context(), p, q)
	if p.creates != 0 || p.reads != 0 || p.kills != 0 {
		t.Fatal("unsupported operation reached provider")
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var decoded response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if reason, ok := providercontract.UnsupportedReason(responseError(decoded), "Initial"); !ok || reason != "fixture_operation_not_supported" {
		t.Fatal(decoded)
	}
	for _, bad := range []response{{ErrorCode: "unsupported"}, {ErrorCode: "unsupported", Unsupported: &providercontract.UnsupportedError{Operation: "Initial", Reason: "private / credential"}}, {Unsupported: out.Unsupported}} {
		if !errors.Is(responseError(bad), sandbox.ErrComputeUnconfirmed) {
			t.Fatal("malformed failure became certainty", bad)
		}
	}
}
func TestUnsupportedProxyRejectsBeforeNodeResolution(t *testing.T) {
	p := (&Hub{}).GenerationProvider(docker.Operations(), func(context.Context, sandbox.Reference) (string, uint64, error) {
		t.Fatal("unsupported call resolved a node")
		return "", 0, nil
	}).(*provider)
	if err := sandbox.ValidateProvider(p); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Initial(t.Context(), reference()); !errors.Is(err, providercontract.ErrUnsupported) {
		t.Fatal(err)
	}
}
func TestNodeOperationMappingCoversForwardedMethods(t *testing.T) {
	for _, method := range []string{"Create", "GetInfo", "Renew", "Kill", "RunCommand", "Initial", "NewCompute", "GetCompute", "RenewCompute", "Suspend", "Resume", "KillCompute", "DeleteRetained", "RunCommandCompute", "ResumeCompute", "Observe"} {
		if wire := operationWire(method); wire == "" || operationMethod(wire) != method {
			t.Fatal(method)
		}
	}
}

// Retained handles and renew are part of the allocation protocol, including on
// nodes whose implementation embeds no optional lifecycle interface.
type retainedWireProvider struct {
	fakeProvider
	renewed sandbox.Compute
	deleted sandbox.RetainedState
}

func (p *retainedWireProvider) ProviderOperations() providercontract.Operations {
	operations := p.fakeProvider.ProviderOperations()
	operations["RenewCompute"] = providercontract.Support{State: providercontract.Supported}
	operations["DeleteRetained"] = providercontract.Support{State: providercontract.Supported}
	return operations
}
func (p *retainedWireProvider) RenewCompute(_ context.Context, _ sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	p.renewed = c
	return sandbox.ComputeState{Compute: c, Status: "running"}, nil
}
func (p *retainedWireProvider) DeleteRetained(_ context.Context, _ sandbox.Reference, retained sandbox.RetainedState) error {
	p.deleted = retained
	return nil
}
func TestRetainedLifecycleDispatchSurvivesStrictWire(t *testing.T) {
	ref := reference()
	retained := sandbox.RetainedState{Reference: ref.AllocationID, ID: uuid.NewString(), OperationID: uuid.NewString(), SourceName: ref.AllocationID, SourceID: "native", Data: "opaque"}
	compute := sandbox.Compute{Generation: 1, Name: ref.AllocationID, ID: "native", RestoredFrom: &retained}
	p := new(retainedWireProvider)
	for _, operation := range []string{"renew_compute", "delete_retained"} {
		q := request{DeploymentGeneration: 1, ID: uuid.NewString(), Sequence: 1, ConnectionID: uuid.NewString(), OwnerEpoch: 1, Operation: operation, TimeoutMillis: 1000, Reference: ref}
		if operation == "renew_compute" {
			q.Compute = &compute
		} else {
			q.Retained = &retained
		}
		raw, err := json.Marshal(frame{Version: ProtocolVersion, Type: "request", Request: &q})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeFrame(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := decoded.Request.validate(); err != nil {
			t.Fatal(err)
		}
		if out := execute(t.Context(), p, *decoded.Request); responseError(out) != nil {
			t.Fatal(out)
		}
	}
	if p.renewed.ID != compute.ID || p.renewed.RestoredFrom == nil || *p.renewed.RestoredFrom != retained || p.deleted != retained {
		t.Fatal("retained evidence lost in transport")
	}
}
