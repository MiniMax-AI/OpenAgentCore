package sandbox

import "errors"

// Node readiness failures. A node probe returns or wraps the one matching its
// first failed check. Only the fixed code crosses the node transport; the error
// text and any wrapped detail, such as host paths or daemon messages, stay local.
var (
	ErrDockerUnavailable                = errors.New("Docker daemon is unavailable")
	ErrDockerLimitsUnsupported          = errors.New("Docker host does not enforce CPU and memory limits")
	ErrRuntimeImageUnavailable          = errors.New("pinned Runtime image is unavailable")
	ErrKVMUnavailable                   = errors.New("KVM is unavailable to sandbox node")
	ErrMicrosandboxArtifactsUnavailable = errors.New("pinned microsandbox artifacts are unavailable")
	ErrCapacityInsufficient             = errors.New("node cannot provide one sandbox of the deployment specification")
)

// NodeProviderUnavailable reports every readiness failure without a fixed cause.
const NodeProviderUnavailable = "provider_unavailable"

var nodeDiagnostics = []struct {
	err  error
	code string
}{
	{ErrDockerUnavailable, "docker_unavailable"},
	{ErrDockerLimitsUnsupported, "docker_limits_unsupported"},
	{ErrRuntimeImageUnavailable, "runtime_image_unavailable"},
	{ErrKVMUnavailable, "kvm_unavailable"},
	{ErrMicrosandboxArtifactsUnavailable, "microsandbox_artifacts_unavailable"},
	{ErrCapacityInsufficient, "capacity_insufficient"},
}

// NodeDiagnostic maps a readiness probe result to its fixed code: empty when
// ready and provider_unavailable when no classified cause is wrapped.
func NodeDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	for _, d := range nodeDiagnostics {
		if errors.Is(err, d.err) {
			return d.code
		}
	}
	return NodeProviderUnavailable
}

// NormalizeNodeDiagnostic keeps an empty or known code. Any other reported value
// becomes provider_unavailable, so Core never stores node-supplied text.
func NormalizeNodeDiagnostic(code string) string {
	if code == "" || code == NodeProviderUnavailable {
		return code
	}
	for _, d := range nodeDiagnostics {
		if code == d.code {
			return code
		}
	}
	return NodeProviderUnavailable
}
