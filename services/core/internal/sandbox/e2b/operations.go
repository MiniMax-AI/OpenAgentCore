package e2b

import (
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
		"ObserveBatch":             {State: providercontract.Supported},
		"DiscoverSelection":        {State: providercontract.Supported},
		"VerifyCredential":         {State: providercontract.Supported},
	}
}
func (*Provider) ProviderOperations() providercontract.Operations { return Operations() }
