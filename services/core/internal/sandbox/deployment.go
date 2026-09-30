package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
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
	CPUs               uint32 `json:"cpus"`
	MemoryMiB          uint32 `json:"memory_mib"`
	RootDiskMiB        uint32 `json:"root_disk_mib,omitempty"`
	EnvironmentDiskMiB uint32 `json:"environment_disk_mib,omitempty"`
}

func (r Resources) ValidatePolicy(provider string, rules DeploymentPolicy) error {
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
	SourceCommit        string `json:"source_commit"`
	ImageID             string `json:"image_id"`
	ImageManifestDigest string `json:"image_manifest_digest"`
	MicrosandboxRef     string `json:"microsandbox_ref"`
	RuntimeSHA256       string `json:"runtime_sha256"`
	FirmwareSHA256      string `json:"firmware_sha256"`
}

func lowerHex(v string, bytes int) bool {
	x, err := hex.DecodeString(v)
	return err == nil && len(x) == bytes && hex.EncodeToString(x) == v
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
	Resources Resources       `json:"resources"`
	Runtime   *RuntimeRelease `json:"runtime,omitempty"`
}

// ValidatePolicy applies a registered adapter's rules without knowing its kind.
func (s DeploymentSpec) ValidatePolicy(provider string, policy DeploymentPolicy) error {
	if err := s.Resources.ValidatePolicy(provider, policy); err != nil {
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

func (s DeploymentSpec) Digest(provider string) string {
	raw, _ := json.Marshal(struct {
		Provider string `json:"provider"`
		DeploymentSpec
	}{provider, s})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
