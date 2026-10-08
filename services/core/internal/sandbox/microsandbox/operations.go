package microsandbox

import "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"

// Operations is this adapter's complete authored resource contract.
func Operations() providercontract.Operations {
	return providercontract.Operations{
		"Create":         {State: providercontract.Supported},
		"GetInfo":        {State: providercontract.Supported},
		"Renew":          {State: providercontract.Supported},
		"Kill":           {State: providercontract.Supported},
		"Initial":        {State: providercontract.Supported},
		"NewCompute":     {State: providercontract.Supported},
		"GetCompute":     {State: providercontract.Supported},
		"Suspend":        {State: providercontract.Supported},
		"Resume":         {State: providercontract.Supported},
		"KillCompute":    {State: providercontract.Supported},
		"DeleteSnapshot": {State: providercontract.Supported},
		"ResumeCompute":  {State: providercontract.Supported},
		"Observe":        {State: providercontract.Supported},
	}
}
func (*Provider) ProviderOperations() providercontract.Operations { return Operations() }
