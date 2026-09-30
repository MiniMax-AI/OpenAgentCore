package deployment

import (
	"math"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// normalizeHealth validates a node's reported health. A diagnostic explains
// unreadiness only: unknown values, including arbitrary text, become
// provider_unavailable, a ready provider has none and empty stays empty.
func normalizeHealth(health NodeHealth) (NodeHealth, error) {
	health.Diagnostic = sandbox.NormalizeNodeDiagnostic(health.Diagnostic)
	if health.ProviderReady {
		health.Diagnostic = ""
	}
	for _, value := range []*int64{health.CPUCount, health.AvailableMemoryBytes, health.AvailableDiskBytes} {
		if value != nil && *value < 0 {
			return health, ErrInvalidInput
		}
	}
	if err := validateHost(health.Host); err != nil {
		return health, err
	}
	return health, nil
}

func validateHost(host *NodeHost) error {
	if host == nil {
		return nil
	}
	if host.ObservedAt == nil || host.ObservedAt.IsZero() || host.ObservedAt.Year() < 1970 || host.ObservedAt.Year() > 9999 {
		return ErrInvalidInput
	}
	for _, value := range []*float64{host.CPUUtilization, host.EffectiveCPUCores} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return ErrInvalidInput
		}
	}
	if host.CPUUtilization != nil && *host.CPUUtilization > 1 || host.EffectiveCPUCores != nil && *host.EffectiveCPUCores == 0 {
		return ErrInvalidInput
	}
	for _, value := range []*int64{host.TotalMemoryBytes, host.AvailableMemoryBytes, host.AvailableDiskBytes} {
		if value != nil && (*value < 0 || *value > 1<<53-1) {
			return ErrInvalidInput
		}
	}
	if host.TotalMemoryBytes != nil && (*host.TotalMemoryBytes == 0 || host.AvailableMemoryBytes != nil && *host.AvailableMemoryBytes > *host.TotalMemoryBytes) {
		return ErrInvalidInput
	}
	return nil
}

// historyPoints fills the window with one point per bucket; a bucket without
// samples has only its start. Neither reads nor offline nodes fill gaps.
func historyPoints(window coremetrics.Range, samples []HostHistoryPoint) []HostHistoryPoint {
	byStart := make(map[time.Time]HostHistoryPoint, len(samples))
	for _, sample := range samples {
		sample.Start = sample.Start.UTC()
		byStart[sample.Start] = sample
	}
	points := make([]HostHistoryPoint, 0)
	step := time.Duration(window.ResolutionSeconds) * time.Second
	for start := window.Start; start.Before(window.End); start = start.Add(step) {
		point, ok := byStart[start]
		if !ok {
			point.Start = start
		}
		points = append(points, point)
	}
	return points
}

// nodeRollout reports a node's preparation of the target generation.
func nodeRollout(n NodeRecord) NodeRollout {
	out := NodeRollout{State: "unknown", ReadyGeneration: n.ReadyGeneration}
	if !n.Online {
		return out
	}
	if n.ProtocolVersion == 1 && n.DeploymentGeneration != n.TargetGeneration {
		out.State = "update_required"
		return out
	}
	switch n.TargetState {
	case "ready", "preparing", "failed":
		out.State = n.TargetState
	}
	if out.State == "failed" && n.TargetDiagnostic != "" {
		out.Diagnostic = sandbox.NormalizeNodeDiagnostic(n.TargetDiagnostic)
	}
	return out
}
