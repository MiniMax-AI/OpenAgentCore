package store

// ProvisioningFailureDetail is private, fixed-category evidence from a confirmed
// initialization receipt. It never contains command text, paths or Runtime output.
type ProvisioningFailureDetail struct {
	Step     *string `json:"step"`
	Index    *int    `json:"index"`
	ExitCode *int    `json:"exit_code"`
}

func (f ProvisioningFailure) detail() *ProvisioningFailureDetail {
	return (ProvisioningFailureDetail{Step: &f.Step, Index: &f.Index, ExitCode: &f.ExitCode}).sanitized()
}

func (f ProvisioningFailureDetail) sanitized() *ProvisioningFailureDetail {
	if f.Step == nil {
		return nil
	}
	result := &ProvisioningFailureDetail{}
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
