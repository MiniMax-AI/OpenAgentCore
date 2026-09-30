package node

import (
	"encoding/hex"
	"math"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

// A control reply grants only its correlated sparse entries. There is no
// complete-inventory or omission-based deletion interpretation.
type generationControl struct {
	ID           string                        `json:"id"`
	Sequence     uint64                        `json:"sequence"`
	ConnectionID string                        `json:"connection_id"`
	OwnerEpoch   uint64                        `json:"owner_epoch"`
	References   []sandbox.GenerationReference `json:"references,omitempty"`
	Retentions   []sandbox.GenerationRetention `json:"retentions,omitempty"`
}

func validGeneration(g uint64) bool { return g > 0 && g <= math.MaxInt64 }
func validSpecificationDigest(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == v
}

func validateVersionFrame(f frame, size int) error {
	if f.Version != ProtocolVersion {
		return sandbox.ErrInvalid
	}
	if f.GenerationManagement && f.Type != "hello" {
		return sandbox.ErrInvalid
	}
	if f.Type != "request" && f.Type != "response" && size > MaxControlFrameBytes {
		return sandbox.ErrInvalid
	}
	if f.Deployment != nil && (!validGeneration(f.Deployment.Generation) || !validSpecificationDigest(f.Deployment.SpecificationDigest) || f.Deployment.ServingGeneration != nil && !validGeneration(*f.Deployment.ServingGeneration)) {
		return sandbox.ErrInvalid
	}
	if f.Health != nil {
		if len(f.Health.Generations) > 8 || !validGenerationHealth(*f.Health) {
			return sandbox.ErrInvalid
		}
		seen := map[uint64]bool{}
		for _, g := range f.Health.Generations {
			if !validGeneration(g.Generation) || !validSpecificationDigest(g.SpecificationDigest) || seen[g.Generation] || (g.State != "ready" && g.State != "preparing" && g.State != "failed") || g.State == "ready" && g.Diagnostic != "" || len(g.Diagnostic) > 64 || sandbox.NormalizeNodeDiagnostic(g.Diagnostic) != g.Diagnostic {
				return sandbox.ErrInvalid
			}
			seen[g.Generation] = true
		}
	}
	if f.Request != nil && (!validGeneration(f.Request.DeploymentGeneration) || !validGeneration(f.Request.Sequence) || !validGeneration(f.Request.OwnerEpoch) || !validID(f.Request.ConnectionID)) {
		return sandbox.ErrInvalid
	}
	if c := f.Control; c != nil {
		if !validID(c.ID) || !validID(c.ConnectionID) || !validGeneration(c.OwnerEpoch) || !validGeneration(c.Sequence) || len(c.References) > 8 || len(c.Retentions) > 8 || c.References != nil && c.Retentions != nil {
			return sandbox.ErrInvalid
		}
		seen := map[uint64]bool{}
		for _, g := range c.References {
			if !validGeneration(g.Generation) || !validSpecificationDigest(g.SpecificationDigest) || seen[g.Generation] {
				return sandbox.ErrInvalid
			}
			seen[g.Generation] = true
		}
		for _, g := range c.Retentions {
			if !validGeneration(g.Generation) || !validSpecificationDigest(g.SpecificationDigest) || seen[g.Generation] {
				return sandbox.ErrInvalid
			}
			seen[g.Generation] = true
		}
	}
	switch f.Type {
	case "hello":
		if !f.GenerationManagement && f.Health != nil && f.Health.Generations != nil {
			return sandbox.ErrInvalid
		}
		if f.Identity == nil || f.Health == nil || f.Request != nil || f.Response != nil || f.Control != nil || f.Deployment != nil {
			return sandbox.ErrInvalid
		}
	case "welcome", "heartbeat_ack":
		if !validID(f.ConnectionID) || !validGeneration(f.OwnerEpoch) || f.Health != nil || f.Identity != nil || f.Request != nil || f.Response != nil || f.Control != nil {
			return sandbox.ErrInvalid
		}
	case "heartbeat":
		if f.Health == nil || !validID(f.ConnectionID) || !validGeneration(f.OwnerEpoch) || f.Identity != nil || f.Request != nil || f.Response != nil || f.Control != nil || f.Deployment != nil {
			return sandbox.ErrInvalid
		}
	case "retention":
		if f.Control == nil || f.Control.References == nil || f.Control.Retentions != nil || f.Deployment != nil || f.Health != nil || f.Identity != nil || f.Request != nil || f.Response != nil {
			return sandbox.ErrInvalid
		}
	case "retention_ack":
		if f.Control == nil || f.Control.References != nil || f.Control.Retentions == nil || f.Deployment == nil || f.Health != nil || f.Identity != nil || f.Request != nil || f.Response != nil {
			return sandbox.ErrInvalid
		}
	case "request":
		if f.Request == nil || f.Response != nil || f.Deployment != nil || f.Control != nil || f.Health != nil || f.Identity != nil {
			return sandbox.ErrInvalid
		}
	case "response":
		if f.Response == nil || f.Request != nil || f.Deployment != nil || f.Control != nil || f.Health != nil || f.Identity != nil {
			return sandbox.ErrInvalid
		}
	default:
		return sandbox.ErrInvalid
	}
	return nil
}

func validGenerationHealth(h Health) bool {
	if h.ObservedAt.IsZero() || h.ObservedAt.Year() < 1970 || h.ObservedAt.Year() > 9999 || h.ActiveOperations < 0 || h.ActiveOperations > maxPending {
		return false
	}
	if len(h.Diagnostic) > 64 || sandbox.NormalizeNodeDiagnostic(h.Diagnostic) != h.Diagnostic {
		return false
	}
	for _, value := range []*int64{h.CPUCount, h.TotalMemoryBytes, h.AvailableMemoryBytes, h.AvailableDiskBytes} {
		if value != nil && (*value < 0 || *value > 1<<53-1) {
			return false
		}
	}
	for _, value := range []*float64{h.CPUUtilization, h.EffectiveCPUCores} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return false
		}
	}
	if h.CPUUtilization != nil && *h.CPUUtilization > 1 || h.EffectiveCPUCores != nil && *h.EffectiveCPUCores == 0 {
		return false
	}
	if h.TotalMemoryBytes != nil && (*h.TotalMemoryBytes == 0 || h.AvailableMemoryBytes != nil && *h.AvailableMemoryBytes > *h.TotalMemoryBytes) {
		return false
	}
	return true
}
