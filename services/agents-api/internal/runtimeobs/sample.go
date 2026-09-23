// Package runtimeobs resolves durable Session identity to provider-owned Runtime
// observations. It is telemetry only and never owns Runtime lifecycle decisions.
package runtimeobs

import (
	"errors"
	"math"
	"time"
)

type Mode string

const (
	ModeNone       Mode = "none"
	ModeSelfHosted Mode = "self_hosted"
	ModeManaged    Mode = "openai_hosted"
)

type Status string

const (
	StatusObserved    Status = "observed"
	StatusUnsupported Status = "unsupported"
	StatusUnavailable Status = "unavailable"
)

// Sample contains provider-neutral cumulative counters and current gauges.
// Pointer fields distinguish an observed zero from an unavailable measurement.
type Sample struct {
	ObservedAt time.Time
	StartedAt  *time.Time

	CPUUsageSecondsTotal *float64
	CPUCapacityCores     *float64
	MemoryUsageBytes     *uint64
	MemoryLimitBytes     *uint64
}

func (s Sample) validate(now time.Time) error {
	if s.ObservedAt.IsZero() || s.ObservedAt.Unix() < 0 || s.ObservedAt.After(now) {
		return errors.New("invalid Runtime observation time")
	}
	if s.StartedAt != nil && (s.StartedAt.IsZero() || s.StartedAt.Unix() < 0 || s.StartedAt.After(s.ObservedAt)) {
		return errors.New("invalid Runtime start time")
	}
	if s.CPUUsageSecondsTotal != nil && (*s.CPUUsageSecondsTotal < 0 || math.IsNaN(*s.CPUUsageSecondsTotal) || math.IsInf(*s.CPUUsageSecondsTotal, 0)) {
		return errors.New("invalid Runtime CPU usage")
	}
	if s.CPUCapacityCores != nil && (*s.CPUCapacityCores <= 0 || math.IsNaN(*s.CPUCapacityCores) || math.IsInf(*s.CPUCapacityCores, 0)) {
		return errors.New("invalid Runtime CPU capacity")
	}
	const maxSafeJSONInteger = uint64(1<<53 - 1)
	if s.MemoryUsageBytes != nil && *s.MemoryUsageBytes > maxSafeJSONInteger {
		return errors.New("Runtime memory usage exceeds the public JSON integer range")
	}
	if s.MemoryLimitBytes != nil && (*s.MemoryLimitBytes == 0 || *s.MemoryLimitBytes > maxSafeJSONInteger) {
		return errors.New("invalid Runtime memory limit")
	}
	return nil
}
