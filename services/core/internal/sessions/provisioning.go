package sessions

import (
	"fmt"
	"slices"
	"time"
)

// EnvironmentFailure is an Environment's recorded provisioning failure. It
// makes the Session failed with this reason and last activity time.
type EnvironmentFailure struct {
	Reason   string                     `json:"reason"`
	FailedAt time.Time                  `json:"failed_at"`
	Detail   *ProvisioningFailureDetail `json:"-"`
}

// ProvisioningFailureDetail is private, fixed-category evidence from a confirmed
// initialization receipt. It never contains command text, paths or Runtime output.
type ProvisioningFailureDetail struct {
	Step     *string               `json:"step"`
	Index    *int                  `json:"index"`
	ExitCode *int                  `json:"exit_code"`
	Category *ProvisioningCategory `json:"category,omitempty"`
}

// ProvisioningFailureReason is the safe reason for an Environment that
// failed without a confirmed failed step: timeouts, unknown effects, missing or
// old receipts, bootstrap rejection and Core restart during initialization.
const ProvisioningFailureReason = "Failed to provision environment: initialization did not complete"

// ProvisioningCategory classifies an Environment that failed without a confirmed
// failed step. It is recorded privately beside the generic public reason.
type ProvisioningCategory string

const (
	// ProvisioningTimeout: the initialization budget or a transfer budget expired.
	ProvisioningTimeout ProvisioningCategory = "timeout"
	// ProvisioningRuntimeDisconnected: the Runtime connection closed or changed.
	ProvisioningRuntimeDisconnected ProvisioningCategory = "runtime_disconnected"
	// ProvisioningUnconfirmed: the effect of an operation is unknown or its receipt is missing or invalid.
	ProvisioningUnconfirmed ProvisioningCategory = "unconfirmed"
	// ProvisioningRejected: the Runtime refused an operation before applying it.
	ProvisioningRejected ProvisioningCategory = "rejected"
	// ProvisioningOperationFailed: the Runtime confirmed failure of an operation with no public step label.
	ProvisioningOperationFailed ProvisioningCategory = "operation_failed"
	// ProvisioningCoreError: Core failed to read, validate or record initialization state.
	ProvisioningCoreError ProvisioningCategory = "core_error"
	// ProvisioningCoreRestart: Core lost ownership of the initialization, by restart or lease loss.
	ProvisioningCoreRestart ProvisioningCategory = "core_restart"
	// ProvisioningComputeReleased: the sandbox was released or never created before initialization completed.
	ProvisioningComputeReleased ProvisioningCategory = "compute_released"
)

// ProvisioningCategories is the one authored category set. The shared fixture
// testdata/provisioning-categories.json checks the client and Web projections.
var ProvisioningCategories = []ProvisioningCategory{
	ProvisioningTimeout, ProvisioningRuntimeDisconnected, ProvisioningUnconfirmed, ProvisioningRejected,
	ProvisioningOperationFailed, ProvisioningCoreError, ProvisioningCoreRestart, ProvisioningComputeReleased,
}

// InitializationLabel is the fixed label of one initialization operation in step timing.
type InitializationLabel string

const (
	InitializationFile      InitializationLabel = "file"
	InitializationSkill     InitializationLabel = "skill"
	InitializationPlugin    InitializationLabel = "plugin"
	InitializationConfigure InitializationLabel = "configure"
	InitializationNPM       InitializationLabel = "npm"
	InitializationPython    InitializationLabel = "python"
	InitializationSetup     InitializationLabel = "setup"
	InitializationFinalize  InitializationLabel = "finalize"
)

// InitializationLabels is the one authored label set; the diagnostics OpenAPI enum is checked against it.
var InitializationLabels = []InitializationLabel{
	InitializationFile, InitializationSkill, InitializationPlugin, InitializationConfigure,
	InitializationNPM, InitializationPython, InitializationSetup, InitializationFinalize,
}

// InitializationStep is the Runtime-reported duration of one initialization
// operation that ran. Step is a fixed label and Index is set for setup commands.
type InitializationStep struct {
	Step       InitializationLabel `json:"step"`
	Index      *int                `json:"index"`
	DurationMS int64               `json:"duration_ms"`
}

// Provisioning step kinds for ProvisioningFailure.Step.
const (
	ProvisioningSetupCommand   = "setup"
	ProvisioningPythonPackages = "python"
	ProvisioningNPMPackages    = "npm"
	ProvisioningInitialFile    = "file"
	ProvisioningSkill          = "skill"
	ProvisioningHarness        = "harness"
)

// ProvisioningFailure identifies a confirmed failed initialization step.
// It cannot carry Runtime output: Step selects a fixed label, Index is the setup
// command position and ExitCode is the Runtime-reported status (0 when absent).
// Category classifies a failure without a confirmed step.
type ProvisioningFailure struct {
	Step     string
	Index    int
	ExitCode int
	Category ProvisioningCategory
}

// Reason renders the public Session error. The setup_commands and Python package
// labels match observed official errors (which append raw pip output for Python;
// Core never does). The npm, file and Skill labels are unverified.
// A script step without a reported exit status keeps the generic reason.
func (f ProvisioningFailure) Reason() string {
	label := map[string]string{
		ProvisioningPythonPackages: "Python package installation",
		ProvisioningNPMPackages:    "npm package installation",
	}[f.Step]
	if f.Step == ProvisioningSetupCommand && f.Index >= 0 {
		label = fmt.Sprintf("setup_commands[%d]", f.Index)
	}
	switch {
	case label != "" && f.ExitCode > 0 && f.ExitCode < 256:
		return fmt.Sprintf("Failed to provision environment: script %q failed with exit code %d", label, f.ExitCode)
	case f.Step == ProvisioningInitialFile:
		return "Failed to provision environment: initial file installation failed"
	case f.Step == ProvisioningHarness:
		return "Failed to prepare environment: the selected Harness is unavailable. Install the supported Harness version on the Runtime and create a new Session."
	case f.Step == ProvisioningSkill:
		return "Failed to provision environment: Skill installation failed"
	}
	return ProvisioningFailureReason
}

// Detail is the private evidence recorded with the failure.
func (f ProvisioningFailure) Detail() *ProvisioningFailureDetail {
	detail := ProvisioningFailureDetail{Step: &f.Step, Index: &f.Index, ExitCode: &f.ExitCode}
	if f.Step == "" {
		detail.Step = nil
		if f.Category != "" {
			detail.Category = &f.Category
		}
	}
	return SanitizedProvisioningDetail(detail)
}

// SanitizedProvisioningDetail keeps only the fields the step's category allows
// and returns nil for a missing or unknown step without a known category.
func SanitizedProvisioningDetail(f ProvisioningFailureDetail) *ProvisioningFailureDetail {
	if f.Step == nil {
		if f.Category == nil || !slices.Contains(ProvisioningCategories, *f.Category) {
			return nil
		}
		value := *f.Category
		return &ProvisioningFailureDetail{Category: &value}
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
