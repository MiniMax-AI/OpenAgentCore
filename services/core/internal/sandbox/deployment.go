package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

type DeploymentMode string

const (
	DeploymentUnconfigured DeploymentMode = ""
	DeploymentNodes        DeploymentMode = "nodes"
	DeploymentDirect       DeploymentMode = "direct"
)

// ValidationError preserves the sandbox error text and identity while identifying
// a fixed configuration field and, for numeric limits, fixed inclusive bounds.
type ValidationError struct {
	Param    string
	Min, Max *uint32
	Message  string
}

func (e *ValidationError) Error() string   { return e.Message }
func (e *ValidationError) Unwrap() error   { return ErrInvalid }
func validationBound(value uint32) *uint32 { return &value }

// Resources describes one managed sandbox, independently of node concurrency.
// Disk bounds are available only where the native provider enforces them.
type Resources struct {
	CPUs               uint32 `json:"cpus" binding:"required"`
	MemoryMiB          uint32 `json:"memory_mib" binding:"required"`
	RootDiskMiB        uint32 `json:"root_disk_mib,omitempty"`
	EnvironmentDiskMiB uint32 `json:"environment_disk_mib,omitempty"`
}

// ValidatePolicy validates the resources against a Provider's declaration. On
// external storage, environment_disk_mib is only the capacity quota request: zero
// requests none and the storage declaration decides whether a quota is possible.
func (r Resources) ValidatePolicy(provider string, rules DeploymentPolicy) error {
	if rules.Workspace != nil && r.EnvironmentDiskMiB == 0 && rules.Disk {
		r.EnvironmentDiskMiB = minimumDiskMiB
	}
	values := reflect.ValueOf(r)
	for i, rule := range resourceContract {
		min, max := rule.Min, rule.Max
		message := rule.Message
		var maximum *uint32 = validationBound(max)
		if rule.OmitZero {
			min, max = 0, 0
			message = provider + " does not support independent disk capacity limits"
			if rules.Disk {
				min, max = minimumDiskMiB, ^uint32(0)
				message = provider + " requires " + rule.Name + " of at least 1024 MiB"
				if rules.Workspace != nil && rule.Name == "environment_disk_mib" {
					message = provider + " requires environment_disk_mib of 0 or at least 1024 MiB"
				}
				maximum = nil
			} else {
				maximum = validationBound(max)
			}
		}
		value := values.Field(i).Uint()
		if value < uint64(min) || value > uint64(max) {
			return &ValidationError{Param: "resources." + rule.Name, Min: validationBound(min), Max: maximum, Message: fmt.Sprintf("%s: %s", ErrInvalid, message)}
		}
	}
	return nil
}

type DeploymentSpec struct {
	Resources Resources `json:"resources" binding:"required"`
	// Workspace is a derived immutable capability receipt, never filesystem configuration.
	Workspace *workspacefs.Declaration `json:"workspace,omitempty" readonly:"true"`
}

// ValidatePolicy applies a registered adapter's rules without knowing its kind.
// A Provider that declares workspace requirements runs every Environment on
// external storage and cannot be deployed without a selection.
func (s DeploymentSpec) ValidatePolicy(provider string, policy DeploymentPolicy) error {
	switch {
	case policy.Workspace == nil && s.Workspace != nil:
		return workspacefs.ErrUnsupported
	case policy.Workspace != nil && s.Workspace == nil:
		return ErrWorkspaceRequired
	case policy.Workspace != nil:
		if err := workspacefs.ValidateCombination(*policy.Workspace, *s.Workspace, s.Resources.EnvironmentDiskMiB); err != nil {
			return err
		}
	}
	return s.Resources.ValidatePolicy(provider, policy)
}

func (s DeploymentSpec) Digest(provider string) string {
	raw, _ := json.Marshal(struct {
		Provider string `json:"provider"`
		DeploymentSpec
	}{provider, s})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// Description is what a provider registration says about a deployment of it:
// its mode and its backend namespace fingerprint.
type Description struct {
	Mode               DeploymentMode
	BackendFingerprint string
}

func BackendFingerprint(kind, namespace string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + namespace))
	return hex.EncodeToString(digest[:])
}
