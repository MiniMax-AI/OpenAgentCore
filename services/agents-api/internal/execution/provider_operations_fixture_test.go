package execution

import (
	"context"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/providercontract"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

func (*lifecycleOnlySandbox) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{
		"Create":            {State: providercontract.Supported},
		"GetInfo":           {State: providercontract.Supported},
		"Renew":             {State: providercontract.Supported},
		"Kill":              {State: providercontract.Supported},
		"RunCommand":        {State: providercontract.Supported},
		"Initial":           {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"NewCompute":        {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"GetCompute":        {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"Suspend":           {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"Resume":            {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"KillCompute":       {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"DeleteSnapshot":    {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"RunCommandCompute": {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"ResumeCompute":     {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"Observe":           {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"ObserveBatch":      {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"DiscoverSelection": {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
		"VerifyCredential":  {State: providercontract.Unsupported, Reason: "fixture_operation_not_supported"},
	}
}
func (*lifecycleOnlySandbox) Initial(context.Context, sandbox.Reference) (sandbox.Compute, error) {
	return sandbox.Compute{}, &providercontract.UnsupportedError{Operation: "Initial", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) NewCompute(context.Context, sandbox.Reference, uint64, *sandbox.SnapshotIdentity) (sandbox.Compute, error) {
	return sandbox.Compute{}, &providercontract.UnsupportedError{Operation: "NewCompute", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) GetCompute(context.Context, sandbox.Reference, sandbox.Compute) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, &providercontract.UnsupportedError{Operation: "GetCompute", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) Suspend(context.Context, sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, &providercontract.UnsupportedError{Operation: "Suspend", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) Resume(context.Context, sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, &providercontract.UnsupportedError{Operation: "Resume", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) KillCompute(context.Context, sandbox.Reference, sandbox.Compute) error {
	return &providercontract.UnsupportedError{Operation: "KillCompute", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) DeleteSnapshot(context.Context, sandbox.Reference, sandbox.SnapshotIdentity) error {
	return &providercontract.UnsupportedError{Operation: "DeleteSnapshot", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) RunCommandCompute(context.Context, sandbox.Reference, sandbox.Compute, sandbox.Command) (sandbox.CommandResult, error) {
	return sandbox.CommandResult{}, &providercontract.UnsupportedError{Operation: "RunCommandCompute", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) ResumeCompute(context.Context, sandbox.Reference, sandbox.Compute) (sandbox.ComputeState, error) {
	return sandbox.ComputeState{}, &providercontract.UnsupportedError{Operation: "ResumeCompute", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) Observe(context.Context, runtimeobs.Target) (runtimeobs.Sample, error) {
	return runtimeobs.Sample{}, &providercontract.UnsupportedError{Operation: "Observe", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) ObserveBatch(context.Context, []runtimeobs.Target) ([]runtimeobs.BatchResult, error) {
	return nil, &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) DiscoverSelection(context.Context, sandbox.Selection) (sandbox.Selection, error) {
	return sandbox.Selection{}, &providercontract.UnsupportedError{Operation: "DiscoverSelection", Reason: "fixture_operation_not_supported"}
}
func (*lifecycleOnlySandbox) VerifyCredential(context.Context, []sandbox.Reference) error {
	return &providercontract.UnsupportedError{Operation: "VerifyCredential", Reason: "fixture_operation_not_supported"}
}
