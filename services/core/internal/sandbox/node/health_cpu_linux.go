//go:build linux

package node

import (
	"math"
	"strconv"
	"strings"
)

// Each connection owns its baseline; missing or reset counters break the interval.
type hostCPUSampler struct {
	previous [8]uint64
	fields   int
}

func (s *hostCPUSampler) sample(read func(string) ([]byte, error)) *float64 {
	data, err := read("/proc/stat")
	if err != nil {
		s.fields = 0
		return nil
	}
	current, fields := parseHostCPU(string(data))
	previous, previousFields := s.previous, s.fields
	s.previous, s.fields = current, fields
	if fields == 0 || previousFields != fields {
		return nil
	}
	var total, idle uint64
	for i, value := range current {
		if value < previous[i] {
			return nil
		}
		delta := value - previous[i]
		if delta > math.MaxUint64-total {
			return nil
		}
		total += delta
		if i == 3 || i == 4 { // Idle and iowait are both non-busy time.
			idle += delta
		}
	}
	if total == 0 {
		return nil
	}
	utilization := float64(total-idle) / float64(total)
	return &utilization
}

func parseHostCPU(data string) ([8]uint64, int) {
	var counters [8]uint64
	line, _, _ := strings.Cut(data, "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return counters, 0
	}
	for i, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return counters, 0
		}
		// Guest and guest_nice are already included in user and nice.
		if i < len(counters) {
			counters[i] = value
		}
	}
	return counters, min(len(fields)-1, len(counters))
}
