package runtimeobs

import (
	"encoding/json"
	"time"
)

// Instance is one provider-owned Runtime incarnation. AllocationID is present
// for managed compute. DeviceID and ConnectionGeneration are reserved for a
// future authenticated self-hosted telemetry source.
type Instance struct {
	AllocationID         string
	ProviderKey          string
	DeviceID             string
	ConnectionGeneration string
	AllocationState      string
	AllocationCreatedAt  time.Time
	ComputePhase         string
	// ProviderState is the provider-owned, persisted compute receipt. It is
	// internal-only and lets an observation source verify the exact current
	// incarnation without accepting provider identifiers from the caller.
	ProviderState json.RawMessage
}

// Target binds telemetry to durable Core identity. A Session is not itself a
// process or sandbox, so callers must retain the complete binding.
type Target struct {
	TenantID, SessionID, EnvironmentID string
	Mode                               Mode
	Instance                           Instance
	// TokenUsage is the latest canonical Core Session Usage snapshot. It is
	// Session-scoped and deliberately independent of the provider Runtime.
	TokenUsage *TokenUsage
}

// TokenUsage contains cumulative measured Session counters. Missing native
// measurements remain nil at the Target level and are never treated as zero.
type TokenUsage struct {
	InputTokens  uint64
	OutputTokens uint64
}
