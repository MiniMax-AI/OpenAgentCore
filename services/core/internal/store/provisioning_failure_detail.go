package store

import "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"

func (f ProvisioningFailure) detail() *sessions.ProvisioningFailureDetail {
	return sanitizedProvisioningDetail(sessions.ProvisioningFailureDetail{Step: &f.Step, Index: &f.Index, ExitCode: &f.ExitCode})
}

func sanitizedProvisioningDetail(f sessions.ProvisioningFailureDetail) *sessions.ProvisioningFailureDetail {
	if f.Step == nil {
		return nil
	}
	result := &sessions.ProvisioningFailureDetail{}
	switch *f.Step {
	case ProvisioningSetupCommand:
		// JSON clients can represent these integer positions exactly.
		if f.Index != nil && *f.Index >= 0 && int64(*f.Index) <= 9007199254740991 {
			value := *f.Index
			result.Index = &value
		}
	case ProvisioningPythonPackages, ProvisioningNPMPackages:
	case ProvisioningInitialFile, ProvisioningSkill, ProvisioningHarness:
		value := *f.Step
		result.Step = &value
		return result
	default:
		return nil
	}
	value := *f.Step
	result.Step = &value
	if f.ExitCode != nil && *f.ExitCode > 0 && *f.ExitCode < 256 {
		value := *f.ExitCode
		result.ExitCode = &value
	}
	return result
}
