package runtimegateway

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

const defaultOwnerLeaseTTL = 90 * time.Second

// DeviceOwnerStore is the DB-backed owner lease surface used by the
// gateway.
type DeviceOwnerStore interface {
	ClaimAgentDaemonDeviceOwner(ctx context.Context, input runtimedevice.ClaimOwner) (runtimedevice.Owner, error)
	RenewAgentDaemonDeviceOwner(ctx context.Context, input runtimedevice.RenewOwner) (runtimedevice.Owner, bool, error)
	ReleaseAgentDaemonDeviceOwner(ctx context.Context, input runtimedevice.ReleaseOwner) (bool, error)
	GetAgentDaemonDeviceOwner(ctx context.Context, deviceID string) (runtimedevice.Owner, bool, error)
}

type ownerLease struct {
	store      DeviceOwnerStore
	deviceID   string
	ownerPodID string
	ownerURL   string
	generation int64
	ttl        time.Duration
}

func normalizeOwnerTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultOwnerLeaseTTL
	}
	return ttl
}
