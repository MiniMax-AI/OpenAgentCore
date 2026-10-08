package sandbox

import (
	"errors"
	"fmt"
)

// Node readiness classes. A node probe returns or wraps the class of its first
// failed check and keeps the Provider's detail, such as host paths or daemon
// messages, in the local error text. Only the class code crosses the node
// transport.
var (
	// The Provider's native service is unreachable or does not answer.
	ErrProviderUnavailable = errors.New("sandbox provider unavailable")
	// The host lacks a capability the Provider requires.
	ErrHostUnsupported = errors.New("host lacks a capability the sandbox provider requires")
	// A pinned native artifact is missing or fails its integrity check.
	ErrArtifactsUnavailable = errors.New("pinned provider artifacts are unavailable")
	// The exact Runtime artifacts could not be transferred or verified.
	ErrRuntimeDownloadFailed = errors.New("Runtime preparation failed")
	// The pinned Runtime image is not available to the Provider.
	ErrRuntimeImageUnavailable = errors.New("pinned Runtime image is unavailable")
	// The host cannot hold one sandbox of the deployment specification.
	ErrCapacityInsufficient = errors.New("node cannot provide one sandbox of the deployment specification")
)

// CheckCapacity reports whether a host can hold one sandbox of the given resources.
func CheckCapacity(r Resources, cpus int, memory uint64) error {
	if cpus < int(r.CPUs) || memory < uint64(r.MemoryMiB)*1024*1024 {
		return fmt.Errorf("%w: one sandbox requires %d CPUs and %d MiB memory; available host capacity is %d CPUs and %d MiB", ErrCapacityInsufficient, r.CPUs, r.MemoryMiB, cpus, memory/1024/1024)
	}
	return nil
}

type NodeDiagnosticCode string

const (
	NodeProviderUnavailable     NodeDiagnosticCode = "provider_unavailable"
	NodeHostUnsupported         NodeDiagnosticCode = "host_unsupported"
	NodeArtifactsUnavailable    NodeDiagnosticCode = "artifacts_unavailable"
	NodeRuntimeDownloadFailed   NodeDiagnosticCode = "runtime_download_failed"
	NodeRuntimeImageUnavailable NodeDiagnosticCode = "runtime_image_unavailable"
	NodeCapacityInsufficient    NodeDiagnosticCode = "capacity_insufficient"
)

var nodeDiagnostics = []struct {
	err  error
	code NodeDiagnosticCode
}{
	{ErrProviderUnavailable, NodeProviderUnavailable},
	{ErrHostUnsupported, NodeHostUnsupported},
	{ErrArtifactsUnavailable, NodeArtifactsUnavailable},
	{ErrRuntimeDownloadFailed, NodeRuntimeDownloadFailed},
	{ErrRuntimeImageUnavailable, NodeRuntimeImageUnavailable},
	{ErrCapacityInsufficient, NodeCapacityInsufficient},
}

// NodeDiagnostic maps a readiness probe result to its class code: empty when
// ready and provider_unavailable when no class is wrapped.
func NodeDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	for _, d := range nodeDiagnostics {
		if errors.Is(err, d.err) {
			return string(d.code)
		}
	}
	return string(NodeProviderUnavailable)
}

// NormalizeNodeDiagnostic keeps an empty or known code. Any other reported value
// becomes provider_unavailable, so Core never stores node-supplied text.
func NormalizeNodeDiagnostic(code string) string {
	if code == "" {
		return code
	}
	for _, d := range nodeDiagnostics {
		if code == string(d.code) {
			return code
		}
	}
	return string(NodeProviderUnavailable)
}
