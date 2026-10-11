package smolvm

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

const noCheckpoint = "smolvm_checkpoint_lifecycle_not_qualified"

func Operations() providercontract.Operations {
	result := providercontract.Operations{}
	for _, operation := range []string{"Create", "GetInfo", "Renew", "Kill", "RunCommand"} {
		result[operation] = providercontract.Support{State: providercontract.Supported}
	}
	for _, operation := range []string{"Initial", "NewCompute", "GetCompute", "Suspend", "Resume", "KillCompute", "DeleteSnapshot", "RunCommandCompute", "ResumeCompute"} {
		result[operation] = providercontract.Support{State: providercontract.Unsupported, Reason: noCheckpoint}
	}
	result["Observe"] = providercontract.Support{State: providercontract.Unsupported, Reason: "smolvm_resource_observation_not_qualified"}
	return result
}
func (*Provider) ProviderOperations() providercontract.Operations { return Operations() }
func unsupported(operation string) error {
	return &providercontract.UnsupportedError{Operation: operation, Reason: Operations()[operation].Reason}
}
func (*Provider) Initial(context.Context, sandbox.Reference) (sandbox.Compute, error) {
	return sandbox.Compute{}, unsupported("Initial")
}
func (*Provider) NewCompute(context.Context, sandbox.Reference, uint64, *sandbox.SnapshotIdentity) (sandbox.Compute, error) {
	return sandbox.Compute{}, unsupported("NewCompute")
}
func (*Provider) GetCompute(context.Context, sandbox.Reference, sandbox.Compute) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, unsupported("GetCompute")
}
func (*Provider) Suspend(context.Context, sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, unsupported("Suspend")
}
func (*Provider) Resume(context.Context, sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, unsupported("Resume")
}
func (*Provider) KillCompute(context.Context, sandbox.Reference, sandbox.Compute) error {
	return unsupported("KillCompute")
}
func (*Provider) DeleteSnapshot(context.Context, sandbox.Reference, sandbox.SnapshotIdentity) error {
	return unsupported("DeleteSnapshot")
}
func (*Provider) RunCommandCompute(context.Context, sandbox.Reference, sandbox.Compute, sandbox.Command) (sandbox.CommandResult, error) {
	return sandbox.CommandResult{}, unsupported("RunCommandCompute")
}
func (*Provider) ResumeCompute(context.Context, sandbox.Reference, sandbox.Compute) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, unsupported("ResumeCompute")
}
func (*Provider) Observe(context.Context, runtimeobs.Target) (runtimeobs.Sample, error) {
	return runtimeobs.Sample{}, unsupported("Observe")
}
