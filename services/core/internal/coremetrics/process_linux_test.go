//go:build linux

package coremetrics

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"
)

// This also runs against real procfs in the Linux package test, without models.
func TestProcessLinuxProbe(t *testing.T) {
	var sampler processSampler
	first := readProcess()
	if first.cpuSeconds == nil || first.rssBytes == nil || *first.rssBytes == 0 {
		t.Fatalf("live process measurements missing: %+v", first)
	}
	if got := sampler.sample(time.Now(), first); got.CPUCores != nil {
		t.Fatal("first CPU interval must be unknown")
	}
	deadline := time.Now().Add(30 * time.Millisecond)
	for time.Now().Before(deadline) {
		runtime.Gosched()
	}
	got := sampler.sample(time.Now(), readProcess())
	if got.CPUCores == nil || *got.CPUCores <= 0 {
		t.Fatal("live process CPU interval missing", got.CPUCores)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(raw))
}
