package runtimeobs

import "time"

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
}

// Target binds telemetry to durable Core identity. A Session is not itself a
// process or sandbox, so callers must retain the complete binding.
type Target struct {
	TenantID, SessionID, EnvironmentID string
	Mode                               Mode
	Instance                           Instance
}
