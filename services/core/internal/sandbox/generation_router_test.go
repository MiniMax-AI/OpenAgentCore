package sandbox_test

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"reflect"
	"testing"
)

type routedProvider struct {
	t         *testing.T
	operation string
	args      []any
	calls     int
	released  *bool
	failure   error
}

func (p *routedProvider) called(operation string, args ...any) error {
	p.t.Helper()
	if operation != p.operation || !reflect.DeepEqual(args, p.args) || *p.released {
		p.t.Fatalf("routed call changed: %s %v", operation, args)
	}
	p.calls++
	return p.failure
}
func (p *routedProvider) ProviderOperations() providercontract.Operations {
	return microsandbox.Operations()
}
func (p *routedProvider) Create(_ context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	return sandbox.Info{}, p.called("Create", b)
}
func (p *routedProvider) GetInfo(_ context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return sandbox.Info{}, p.called("GetInfo", r)
}
func (p *routedProvider) Renew(_ context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return sandbox.Info{}, p.called("Renew", r)
}
func (p *routedProvider) Kill(_ context.Context, r sandbox.Reference) error {
	return p.called("Kill", r)
}
func (p *routedProvider) Observe(_ context.Context, t runtimeobs.Target) (runtimeobs.Sample, error) {
	return runtimeobs.Sample{}, p.called("Observe", t)
}
func (p *routedProvider) Initial(_ context.Context, r sandbox.Reference) (sandbox.Compute, error) {
	return sandbox.Compute{}, p.called("Initial", r)
}
func (p *routedProvider) NewCompute(_ context.Context, r sandbox.Reference, g uint64, s *sandbox.SnapshotIdentity) (sandbox.Compute, error) {
	return sandbox.Compute{}, p.called("NewCompute", r, g, s)
}
func (p *routedProvider) GetCompute(_ context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, p.called("GetCompute", r, c)
}
func (p *routedProvider) Suspend(_ context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, p.called("Suspend", q)
}
func (p *routedProvider) Resume(_ context.Context, q sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, p.called("Resume", q)
}
func (p *routedProvider) KillCompute(_ context.Context, r sandbox.Reference, c sandbox.Compute) error {
	return p.called("KillCompute", r, c)
}
func (p *routedProvider) DeleteSnapshot(_ context.Context, r sandbox.Reference, s sandbox.SnapshotIdentity) error {
	return p.called("DeleteSnapshot", r, s)
}
func (p *routedProvider) ResumeCompute(_ context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, p.called("ResumeCompute", r, c)
}

func TestGenerationRouterRoutesEveryOperationAndReleasesCalls(t *testing.T) {
	ref := sandbox.Reference{TenantID: "tenant", EnvironmentID: "environment", AllocationID: "allocation"}
	compute := sandbox.Compute{ID: "compute", Generation: 7}
	snapshot := sandbox.SnapshotIdentity{}
	target := runtimeobs.Target{TenantID: ref.TenantID, EnvironmentID: ref.EnvironmentID, Instance: runtimeobs.Instance{AllocationID: ref.AllocationID}}
	for _, test := range []struct {
		name string
		args []any
		call func(sandbox.SandboxProvider) error
	}{
		{"Create", []any{sandbox.Bootstrap{Reference: ref}}, func(p sandbox.SandboxProvider) error {
			_, err := p.Create(t.Context(), sandbox.Bootstrap{Reference: ref})
			return err
		}},
		{"GetInfo", []any{ref}, func(p sandbox.SandboxProvider) error { _, err := p.GetInfo(t.Context(), ref); return err }},
		{"Renew", []any{ref}, func(p sandbox.SandboxProvider) error { _, err := p.Renew(t.Context(), ref); return err }},
		{"Kill", []any{ref}, func(p sandbox.SandboxProvider) error { return p.Kill(t.Context(), ref) }},
		{"Observe", []any{target}, func(p sandbox.SandboxProvider) error { _, err := p.Observe(t.Context(), target); return err }},
		{"Initial", []any{ref}, func(p sandbox.SandboxProvider) error { _, err := p.Initial(t.Context(), ref); return err }},
		{"NewCompute", []any{ref, uint64(9), &snapshot}, func(p sandbox.SandboxProvider) error {
			_, err := p.NewCompute(t.Context(), ref, uint64(9), &snapshot)
			return err
		}},
		{"GetCompute", []any{ref, compute}, func(p sandbox.SandboxProvider) error { _, err := p.GetCompute(t.Context(), ref, compute); return err }},
		{"Suspend", []any{sandbox.SuspendRequest{Reference: ref}}, func(p sandbox.SandboxProvider) error {
			_, err := p.Suspend(t.Context(), sandbox.SuspendRequest{Reference: ref})
			return err
		}},
		{"Resume", []any{sandbox.ResumeRequest{Reference: ref}}, func(p sandbox.SandboxProvider) error {
			_, err := p.Resume(t.Context(), sandbox.ResumeRequest{Reference: ref})
			return err
		}},
		{"KillCompute", []any{ref, compute}, func(p sandbox.SandboxProvider) error { return p.KillCompute(t.Context(), ref, compute) }},
		{"DeleteSnapshot", []any{ref, snapshot}, func(p sandbox.SandboxProvider) error { return p.DeleteSnapshot(t.Context(), ref, snapshot) }},
		{"ResumeCompute", []any{ref, compute}, func(p sandbox.SandboxProvider) error {
			_, err := p.ResumeCompute(t.Context(), ref, compute)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, failure := range []error{nil, errors.New("native failure")} {
				released := false
				native := &routedProvider{t: t, operation: test.name, args: test.args, released: &released, failure: failure}
				resolutions := 0
				operations := microsandbox.Operations()
				router := sandbox.NewGenerationRouter(operations, func(_ context.Context, got sandbox.Reference) (sandbox.SandboxProvider, func(), error) {
					if got != ref {
						t.Fatal("wrong allocation", got)
					}
					resolutions++
					return native, func() {
						if released {
							t.Error("double release")
						}
						released = true
					}, nil
				})
				operations[test.name] = providercontract.Support{State: providercontract.Unsupported, Reason: "test_rejection"}
				if err := test.call(router); !errors.Is(err, failure) || resolutions != 1 || native.calls != 1 || !released {
					t.Fatal("lost outcome or release", err, resolutions, native.calls, released)
				}
				router = sandbox.NewGenerationRouter(operations, func(context.Context, sandbox.Reference) (sandbox.SandboxProvider, func(), error) {
					t.Fatal("unsupported operation resolved")
					return nil, nil, nil
				})
				if err := test.call(router); !errors.Is(err, providercontract.ErrUnsupported) {
					t.Fatal(err)
				}
				resolutionFailure := errors.New("allocation lookup failed")
				router = sandbox.NewGenerationRouter(microsandbox.Operations(), func(context.Context, sandbox.Reference) (sandbox.SandboxProvider, func(), error) {
					return nil, nil, resolutionFailure
				})
				if err := test.call(router); !errors.Is(err, resolutionFailure) {
					t.Fatal("lost resolution error", err)
				}
			}
		})
	}
}
