package deployment

import (
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestRecommendCapacity(t *testing.T) {
	const gib = int64(1) << 30
	size := sandbox.Resources{CPUs: 2, MemoryMiB: 4096, RootDiskMiB: 8192}
	host := func(cpus int64, cores *float64, memory, disk int64) NodeHealth {
		now := time.Now()
		return NodeHealth{CPUCount: &cpus, AvailableDiskBytes: &disk, Host: &NodeHost{EffectiveCPUCores: cores, TotalMemoryBytes: &memory, AvailableDiskBytes: &disk, ObservedAt: &now}}
	}
	limited := 1.0
	for _, c := range []struct {
		name       string
		host       NodeHealth
		checkpoint bool
		want       Capacity
	}{
		{"checkpoint host", host(4, nil, 14*gib+gib*4/10, 239*gib), true, Capacity{MaxActive: 3, MaxRetained: 14}},
		{"no checkpoint", host(4, nil, 14*gib+gib*4/10, 239*gib), false, Capacity{MaxActive: 3, MaxRetained: 3}},
		{"just enough for one", host(1, nil, 6*gib, 1*gib), true, Capacity{MaxActive: 1, MaxRetained: 1}},
		{"cgroup limit wins", host(64, &limited, 256*gib, 1000*gib), true, Capacity{MaxActive: 1, MaxRetained: 62}},
		{"memory bound", host(64, nil, 10*gib, 100*gib), false, Capacity{MaxActive: 2, MaxRetained: 2}},
	} {
		if got, ok := RecommendCapacity(c.host, size, c.checkpoint); !ok || got != c.want {
			t.Errorf("%s: %v %v, want %v", c.name, got, ok, c.want)
		}
	}
	if _, ok := RecommendCapacity(NodeHealth{}, size, true); ok {
		t.Error("recommended without host measurements")
	}
}
