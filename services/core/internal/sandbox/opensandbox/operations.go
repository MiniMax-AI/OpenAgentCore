package opensandbox

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// Operations is this adapter's complete authored resource contract. OpenSandbox
// pause is specific to its runtime, so the checkpoint lifecycle stays unsupported.
func Operations() providercontract.Operations {
	operations := providercontract.Operations{}
	for _, name := range []string{"Create", "GetInfo", "Renew", "Kill", "RunCommand", "Observe"} {
		operations[name] = providercontract.Support{State: providercontract.Supported}
	}
	for _, name := range []string{"Initial", "NewCompute", "GetCompute", "Suspend", "Resume", "KillCompute", "DeleteSnapshot", "RunCommandCompute", "ResumeCompute", "RenewCompute"} {
		operations[name] = providercontract.Support{State: providercontract.Unsupported, Reason: "opensandbox_pause_is_runtime_specific"}
	}
	for _, name := range []string{"RestorePreparation", "SavePreparation"} {
		operations[name] = providercontract.Support{State: providercontract.Unsupported, Reason: "opensandbox_has_no_preparation_cache"}
	}
	return operations
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
func (*Provider) RenewCompute(context.Context, sandbox.Reference, sandbox.Compute) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, unsupported("RenewCompute")
}
func (*Provider) RestorePreparation(context.Context, sandbox.Reference, string) (bool, error) {
	return false, unsupported("RestorePreparation")
}
func (*Provider) SavePreparation(context.Context, sandbox.Reference, string) error {
	return unsupported("SavePreparation")
}
