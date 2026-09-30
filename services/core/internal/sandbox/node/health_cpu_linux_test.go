//go:build linux

package node

import (
	"math"
	"os"
	"testing"
)

func TestHostCPUUtilization(t *testing.T) {
	var sampler hostCPUSampler
	sample := func(data string) *float64 {
		return sampler.sample(func(string) ([]byte, error) { return []byte(data), nil })
	}
	if sample("cpu 100 20 30 400 50 10 20 5 60 10\ncpu0 9999") != nil {
		t.Fatal("first sample must be unknown")
	}
	// Busy increases 40, idle+iowait increases 60. Guest deltas must not be added.
	if got := sample("cpu 120 20 40 450 60 15 25 5 80 10"); got == nil || math.Abs(*got-.4) > 1e-12 {
		t.Fatalf("host busy ratio = %v, want .4", got)
	}
	if got := sample("cpu 120 20 40 460 60 15 25 5 80 10"); got == nil || *got != 0 {
		t.Fatalf("idle host = %v, want known zero", got)
	}
	if got := sample("cpu 130 20 40 460 60 15 25 5 80 10"); got == nil || *got != 1 {
		t.Fatalf("busy host = %v, want one", got)
	}
}

func TestHostCPUUnknownAndReset(t *testing.T) {
	for _, tc := range []struct {
		name, middle string
		err          error
	}{
		{"read-error", "", os.ErrPermission},
		{"missing", "intr 100", nil},
		{"malformed", "cpu 20 bad 20 20", nil},
		{"negative", "cpu -1 20 20 20", nil},
		{"overflow", "cpu 18446744073709551616 20 20 20", nil},
		{"reset", "cpu 5 5 5 5", nil},
		{"unchanged", "cpu 10 10 10 10", nil},
		{"field-change", "cpu 20 20 20 20 20", nil},
		{"delta-overflow", "cpu 18446744073709551615 18446744073709551615 20 20", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sampler hostCPUSampler
			read := func(string) ([]byte, error) { return []byte("cpu 10 10 10 10"), nil }
			sampler.sample(read)
			if got := sampler.sample(func(string) ([]byte, error) { return []byte(tc.middle), tc.err }); got != nil {
				t.Fatalf("invalid interval returned %g", *got)
			}
			if tc.err != nil || tc.name == "missing" || tc.name == "malformed" || tc.name == "negative" || tc.name == "overflow" {
				if sampler.sample(read) != nil {
					t.Fatal("missing evidence must break the interval")
				}
			}
		})
	}
}

func TestHostCPUCountersCannotDecreaseEvenIfTotalIncreases(t *testing.T) {
	var sampler hostCPUSampler
	sampler.sample(func(string) ([]byte, error) { return []byte("cpu 100 10 10 10 10"), nil })
	if got := sampler.sample(func(string) ([]byte, error) { return []byte("cpu 200 10 10 10 9"), nil }); got != nil {
		t.Fatal("decreasing iowait counter was accepted")
	}
	if got := sampler.sample(func(string) ([]byte, error) { return []byte("cpu 210 10 10 10 9"), nil }); got == nil || *got != 1 {
		t.Fatal("valid interval after reset did not recover")
	}
}
