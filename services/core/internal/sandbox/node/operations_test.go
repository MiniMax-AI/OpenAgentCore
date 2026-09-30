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
	p := (&Hub{}).GenerationProvider("docker", docker.Operations(), func(context.Context, sandbox.Reference) (string, uint64, error) {
		t.Fatal("unsupported call resolved a node")
		return "", 0, nil
	}).(*provider)
	if err := sandbox.ValidateProvider(p); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Initial(t.Context(), reference()); !errors.Is(err, providercontract.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := p.ObserveBatch(t.Context(), nil); !errors.Is(err, providercontract.ErrUnsupported) {
		t.Fatal(err)
	}
}
func TestNodeOperationMappingCoversForwardedMethods(t *testing.T) {
	for _, method := range []string{"Create", "GetInfo", "Renew", "Kill", "RunCommand", "Initial", "NewCompute", "GetCompute", "Suspend", "Resume", "KillCompute", "DeleteSnapshot", "RunCommandCompute", "ResumeCompute", "Observe"} {
		if wire := operationWire(method); wire == "" || operationMethod(wire) != method {
			t.Fatal(method)
		}
	}
}
