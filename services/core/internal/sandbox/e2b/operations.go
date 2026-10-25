package e2b

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// Operations is this adapter's complete authored resource contract.
func Operations() providercontract.Operations {
	return providercontract.Operations{
		"Create":             {State: providercontract.Supported},
		"GetInfo":            {State: providercontract.Supported},
		"Renew":              {State: providercontract.Supported},
		"Kill":               {State: providercontract.Supported},
		"RunCommand":         {State: providercontract.Supported},
		"Initial":            {State: providercontract.Supported},
		"NewCompute":         {State: providercontract.Supported},
		"GetCompute":         {State: providercontract.Supported},
		"Suspend":            {State: providercontract.Supported},
		"Resume":             {State: providercontract.Supported},
		"KillCompute":        {State: providercontract.Supported},
		"DeleteSnapshot":     {State: providercontract.Supported},
		"RunCommandCompute":  {State: providercontract.Supported},
		"RenewCompute":       {State: providercontract.Supported},
		"ResumeCompute":      {State: providercontract.Supported},
		"RestorePreparation": {State: providercontract.Unsupported, Reason: "e2b_does_not_support_preparation_cache"},
		"SavePreparation":    {State: providercontract.Unsupported, Reason: "e2b_does_not_support_preparation_cache"},
		"Observe":            {State: providercontract.Supported},
	}
}
func (*Provider) ProviderOperations() providercontract.Operations { return Operations() }
func (p *Provider) RestorePreparation(context.Context, sandbox.Reference, string) (bool, error) {
	return false, &providercontract.UnsupportedError{Operation: "RestorePreparation", Reason: Operations()["RestorePreparation"].Reason}
}
func (p *Provider) SavePreparation(context.Context, sandbox.Reference, string) error {
	return &providercontract.UnsupportedError{Operation: "SavePreparation", Reason: Operations()["SavePreparation"].Reason}
}
