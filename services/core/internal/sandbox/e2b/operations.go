package e2b

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

var _ sandbox.CheckpointProvider = (*Provider)(nil)
var _ sandbox.SelectionDiscoverer = (*Provider)(nil)
var _ sandbox.CredentialVerifier = (*Provider)(nil)
var _ runtimeobs.BatchSource = (*Provider)(nil)

// Operations is this adapter's complete authored resource contract.
func Operations() providercontract.Operations {
	return providercontract.Operations{
		"Create":            {State: providercontract.Supported},
		"GetInfo":           {State: providercontract.Supported},
		"Renew":             {State: providercontract.Supported},
		"Kill":              {State: providercontract.Supported},
		"RunCommand":        {State: providercontract.Supported},
		"Initial":           {State: providercontract.Unsupported, Reason: "e2b_does_not_support_checkpoints"},
		"NewCompute":        {State: providercontract.Unsupported, Reason: "e2b_does_not_support_checkpoints"},
		"GetCompute":        {State: providercontract.Unsupported, Reason: "e2b_does_not_support_checkpoints"},
		"Suspend":           {State: providercontract.Unsupported, Reason: "e2b_does_not_support_checkpoints"},
		"Resume":            {State: providercontract.Unsupported, Reason: "e2b_does_not_support_checkpoints"},
		"KillCompute":       {State: providercontract.Unsupported, Reason: "e2b_does_not_support_checkpoints"},
		"DeleteSnapshot":    {State: providercontract.Unsupported, Reason: "e2b_does_not_support_checkpoints"},
		"RunCommandCompute": {State: providercontract.Unsupported, Reason: "e2b_does_not_support_checkpoints"},
		"ResumeCompute":     {State: providercontract.Unsupported, Reason: "e2b_does_not_support_checkpoints"},
		"Observe":           {State: providercontract.Supported},
		"ObserveBatch":      {State: providercontract.Supported},
		"DiscoverSelection": {State: providercontract.Supported},
		"VerifyCredential":  {State: providercontract.Supported},
	}
}
func (*Provider) ProviderOperations() providercontract.Operations { return Operations() }
func (p *Provider) Initial(context.Context, sandbox.Reference) (sandbox.Compute, error) {
	return sandbox.Compute{}, &providercontract.UnsupportedError{Operation: "Initial", Reason: Operations()["Initial"].Reason}
}
func (p *Provider) NewCompute(context.Context, sandbox.Reference, uint64, *sandbox.SnapshotIdentity) (sandbox.Compute, error) {
	return sandbox.Compute{}, &providercontract.UnsupportedError{Operation: "NewCompute", Reason: Operations()["NewCompute"].Reason}
}
func (p *Provider) GetCompute(context.Context, sandbox.Reference, sandbox.Compute) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, &providercontract.UnsupportedError{Operation: "GetCompute", Reason: Operations()["GetCompute"].Reason}
}
func (p *Provider) Suspend(context.Context, sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, &providercontract.UnsupportedError{Operation: "Suspend", Reason: Operations()["Suspend"].Reason}
}
func (p *Provider) Resume(context.Context, sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, &providercontract.UnsupportedError{Operation: "Resume", Reason: Operations()["Resume"].Reason}
}
func (p *Provider) KillCompute(context.Context, sandbox.Reference, sandbox.Compute) error {
	return &providercontract.UnsupportedError{Operation: "KillCompute", Reason: Operations()["KillCompute"].Reason}
}
func (p *Provider) DeleteSnapshot(context.Context, sandbox.Reference, sandbox.SnapshotIdentity) error {
	return &providercontract.UnsupportedError{Operation: "DeleteSnapshot", Reason: Operations()["DeleteSnapshot"].Reason}
}
func (p *Provider) RunCommandCompute(context.Context, sandbox.Reference, sandbox.Compute, sandbox.Command) (sandbox.CommandResult, error) {
	return sandbox.CommandResult{}, &providercontract.UnsupportedError{Operation: "RunCommandCompute", Reason: Operations()["RunCommandCompute"].Reason}
}
func (p *Provider) ResumeCompute(context.Context, sandbox.Reference, sandbox.Compute) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, &providercontract.UnsupportedError{Operation: "ResumeCompute", Reason: Operations()["ResumeCompute"].Reason}
}
