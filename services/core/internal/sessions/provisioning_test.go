package sessions

import "testing"

// Reasons contain only a fixed label and an exit status. Setup and Python labels
// match official samples; npm, system, file and Skill labels are unverified.
func TestProvisioningFailureReasons(t *testing.T) {
	for failure, want := range map[ProvisioningFailure]string{
		{Step: ProvisioningSetupCommand, Index: 0, ExitCode: 3}:  `Failed to provision environment: script "setup_commands[0]" failed with exit code 3`,
		{Step: ProvisioningSetupCommand, Index: 12, ExitCode: 1}: `Failed to provision environment: script "setup_commands[12]" failed with exit code 1`,
		{Step: ProvisioningPythonPackages, ExitCode: 1}:          `Failed to provision environment: script "Python package installation" failed with exit code 1`,
		{Step: ProvisioningNPMPackages, ExitCode: 1}:             `Failed to provision environment: script "npm package installation" failed with exit code 1`,
		{Step: ProvisioningInitialFile}:                          "Failed to provision environment: initial file installation failed",
		{Step: ProvisioningSkill}:                                "Failed to provision environment: Skill installation failed",
		// Missing or impossible statuses, unknown steps and old receipts stay generic.
		{Step: ProvisioningSetupCommand, Index: 0}:               ProvisioningFailureReason,
		{Step: ProvisioningSetupCommand, Index: -1, ExitCode: 3}: ProvisioningFailureReason,
		{Step: ProvisioningPythonPackages, ExitCode: 256}:        ProvisioningFailureReason,
		{Step: ProvisioningNPMPackages, ExitCode: -9}:            ProvisioningFailureReason,
		{Step: "configure", ExitCode: 1}:                         ProvisioningFailureReason,
		{}:                                                       ProvisioningFailureReason,
	} {
		if got := failure.Reason(); got != want || len(got) > 256 {
			t.Errorf("%+v: %q", failure, got)
		}
	}
}
