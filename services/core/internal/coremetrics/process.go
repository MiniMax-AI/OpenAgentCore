package coremetrics

import (
	"math"
	"time"
)

type processReading struct {
	cpuSeconds       *float64
	cpuLimitCores    *float64
	rssBytes         *uint64
	memoryLimitBytes *uint64
}

// The existing sampling loop owns the baseline. Reads never advance it.
type processSampler struct {
	at         time.Time
	cpuSeconds *float64
}

func (s *processSampler) sample(at time.Time, reading processReading) Process {
	result := Process{CPULimitCores: reading.cpuLimitCores, RSSBytes: reading.rssBytes, MemoryLimitBytes: reading.memoryLimitBytes}
	cpu := reading.cpuSeconds
	if cpu != nil && (*cpu < 0 || math.IsNaN(*cpu) || math.IsInf(*cpu, 0)) {
		cpu = nil
	}
	elapsed := at.Sub(s.at)
	if cpu != nil && s.cpuSeconds != nil && !s.at.IsZero() && elapsed > 0 && elapsed <= 2*SampleInterval && *cpu >= *s.cpuSeconds {
		result.CPUCores = ptr((*cpu - *s.cpuSeconds) / elapsed.Seconds())
	}
	// Counter resets, missing observations and invalid gaps start a new interval.
	s.at, s.cpuSeconds = at, cpu
	return result
}
