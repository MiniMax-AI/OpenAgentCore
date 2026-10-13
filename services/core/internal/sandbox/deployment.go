package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"

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

func (r Resources) ValidatePolicy(provider string, rules DeploymentPolicy) error {
	return r.ValidateWorkspacePolicy(provider, rules, nil)
}

func (r Resources) validatePolicy(provider string, rules DeploymentPolicy) error {
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
				message = provider + " requires root_disk_mib and environment_disk_mib of at least 1024 MiB"
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

// RuntimeRelease preserves the identities of one verified distribution. Docker
// may address the same archive by config ID or OCI manifest digest; microsandbox
// has its own imported OCI identity. These are not interchangeable hashes.
type RuntimeRelease struct {
	SourceCommit        string `json:"source_commit" binding:"required"`
	ImageID             string `json:"image_id" binding:"required"`
	ImageManifestDigest string `json:"image_manifest_digest" binding:"required"`
	MicrosandboxRef     string `json:"microsandbox_ref" binding:"required"`
	RuntimeSHA256       string `json:"runtime_sha256" binding:"required"`
	FirmwareSHA256      string `json:"firmware_sha256" binding:"required"`
}

func (r RuntimeRelease) Validate() error {
	values := reflect.ValueOf(r)
	for i, rule := range runtimeContract {
		if !regexp.MustCompile("^(?:" + rule.Pattern + ")$").MatchString(values.Field(i).String()) {
			return &ValidationError{Param: "runtime", Message: fmt.Sprintf("%s: Runtime must reference one immutable distribution", ErrInvalid)}
		}
	}
	return nil
}

type DeploymentSpec struct {
	Resources Resources       `json:"resources" binding:"required"`
	Runtime   *RuntimeRelease `json:"runtime,omitempty"`
	// Workspace is a derived immutable capability receipt, never filesystem configuration.
	Workspace *workspacefs.Declaration `json:"workspace,omitempty" readonly:"true"`
}

// ValidatePolicy applies a registered adapter's rules without knowing its kind.
func (s DeploymentSpec) ValidatePolicy(provider string, policy DeploymentPolicy) error {
	return s.ValidateWorkspacePolicy(provider, policy, s.Workspace)
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

// ValidateWorkspacePolicy validates explicit external storage without weakening root disk limits.
func (r Resources) ValidateWorkspacePolicy(provider string, rules DeploymentPolicy, declaration *workspacefs.Declaration) error {
	if declaration == nil {
		return r.validatePolicy(provider, rules)
	}
	if rules.Workspace == nil {
		return workspacefs.ErrUnsupported
	}
	if err := workspacefs.ValidateCombination(*rules.Workspace, *declaration, r.EnvironmentDiskMiB); err != nil {
		return err
	}
	checked := r
	if checked.EnvironmentDiskMiB == 0 && rules.Disk {
		checked.EnvironmentDiskMiB = minimumDiskMiB
	}
	return checked.validatePolicy(provider, rules)
}
func (s DeploymentSpec) ValidateWorkspacePolicy(provider string, policy DeploymentPolicy, declaration *workspacefs.Declaration) error {
	if err := s.Resources.ValidateWorkspacePolicy(provider, policy, declaration); err != nil {
		return err
	}

	if !policy.Runtime {
		if s.Runtime != nil {
			return &ValidationError{Param: "runtime", Message: fmt.Sprintf("%s: %s", ErrInvalid, policy.RuntimeError)}
		}
		return nil
	}
	if s.Runtime == nil {
		return &ValidationError{Param: "runtime", Message: fmt.Sprintf("%s: managed nodes require a pinned Runtime release", ErrInvalid)}
	}
	return s.Runtime.Validate()
}
