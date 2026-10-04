package microsandbox

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

var _ sandbox.SuspensionProvider = (*Provider)(nil)
var _ sandbox.SelectionDiscoverer = (*Provider)(nil)
var _ sandbox.CredentialVerifier = (*Provider)(nil)
var _ runtimeobs.BatchSource = (*Provider)(nil)

// Operations is this adapter's complete authored resource contract.
func Operations() providercontract.Operations {
	return providercontract.Operations{
		"Create":                   {State: providercontract.Supported},
		"GetInfo":                  {State: providercontract.Supported},
		"Renew":                    {State: providercontract.Supported},
		"Kill":                     {State: providercontract.Supported},
		"RunCommand":               {State: providercontract.Supported},
		"Initial":                  {State: providercontract.Supported},
		"NewCompute":               {State: providercontract.Supported},
		"RenewCompute":             {State: providercontract.Supported},
		"GetCompute":               {State: providercontract.Supported},
		"Suspend":                  {State: providercontract.Supported},
		"Resume":                   {State: providercontract.Supported},
		"KillCompute":              {State: providercontract.Supported},
		"DeleteRetained":           {State: providercontract.Supported},
		"RunCommandCompute":        {State: providercontract.Supported},
		"ResumeCompute":            {State: providercontract.Supported},
		"ObservationProviderType":  {State: providercontract.Supported},
		"ResolveObservationSource": {State: providercontract.Supported},
		"Observe":                  {State: providercontract.Supported},
		"ObserveBatch":             {State: providercontract.Unsupported, Reason: "microsandbox_does_not_support_batch_observation"},
		"DiscoverSelection":        {State: providercontract.Unsupported, Reason: "microsandbox_does_not_support_selection_discovery"},
		"VerifyCredential":         {State: providercontract.Unsupported, Reason: "microsandbox_does_not_support_credential_verification"},
	}
}
func (*Provider) ProviderOperations() providercontract.Operations { return Operations() }
func (p *Provider) ObserveBatch(context.Context, []runtimeobs.Target) ([]runtimeobs.BatchResult, error) {
	return nil, &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: Operations()["ObserveBatch"].Reason}
}
func (p *Provider) DiscoverSelection(context.Context, sandbox.Selection) (sandbox.Selection, error) {
	return sandbox.Selection{}, &providercontract.UnsupportedError{Operation: "DiscoverSelection", Reason: Operations()["DiscoverSelection"].Reason}
}
func (p *Provider) VerifyCredential(context.Context, []sandbox.Reference) error {
	return &providercontract.UnsupportedError{Operation: "VerifyCredential", Reason: Operations()["VerifyCredential"].Reason}
}
