//go:build linux

package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const gib int64 = 1024 * 1024 * 1024

func hostFixture(t *testing.T, files map[string]string) func(string) ([]byte, error) {
	t.Helper()
	root := t.TempDir()
	for name, value := range files {
		name = filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(root, name)) }
}

func v2HostFixture() map[string]string {
	return map[string]string{
		"/proc/meminfo":                      "MemTotal: 8388608 kB\nMemAvailable: 3145728 kB\n",
		"/proc/self/cgroup":                  "0::/parent/leaf\n",
		"/proc/self/mountinfo":               "35 25 0:30 / /sys/fs/cgroup rw - cgroup2 cgroup2 rw\n",
		"/sys/fs/cgroup/cgroup.controllers":  "cpu memory cpuset\n",
		"/sys/fs/cgroup/parent/cpu.max":      "150000 100000\n",
		"/sys/fs/cgroup/parent/leaf/cpu.max": "max 100000\n",
	}
}

func TestHostResourcesV2(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]string)
		cpus   int
		want   float64
	}{
		{"inherited", nil, 4, 1.5},
		{"leaf", func(f map[string]string) { f["/sys/fs/cgroup/parent/leaf/cpu.max"] = "25000 100000" }, 4, .25},
		{"affinity", nil, 1, 1},
		{"unlimited", func(f map[string]string) { f["/sys/fs/cgroup/parent/cpu.max"] = "max 100000" }, 4, 4},
		{"controller-disabled-at-leaf", func(f map[string]string) {
			delete(f, "/sys/fs/cgroup/parent/leaf/cpu.max")
			f["/sys/fs/cgroup/parent/leaf/cgroup.controllers"] = ""
		}, 4, 1.5},
		{"root", func(f map[string]string) { f["/proc/self/cgroup"] = "0::/\n" }, 4, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := v2HostFixture()
			if tc.change != nil {
				tc.change(files)
			}
			var h Health
			fillHostResources(&h, hostFixture(t, files), tc.cpus)
			if h.TotalMemoryBytes == nil || *h.TotalMemoryBytes != 8*gib || h.AvailableMemoryBytes == nil || *h.AvailableMemoryBytes != 3*gib {
				t.Fatalf("host memory = %+v", h)
			}
			if h.EffectiveCPUCores == nil || *h.EffectiveCPUCores != tc.want {
				t.Fatalf("effective CPU = %v, want %g", h.EffectiveCPUCores, tc.want)
			}
		})
	}
}

func TestHostResourcesUnknownLimits(t *testing.T) {
	for _, tc := range []struct {
		name, file, value string
		remove            bool
	}{
		{"membership-missing", "/proc/self/cgroup", "", true},
		{"membership-invalid", "/proc/self/cgroup", "0::/../bad", false},
		{"membership-invalid-id", "/proc/self/cgroup", "bad:pids:/parent/leaf", false},
		{"membership-duplicate", "/proc/self/cgroup", "0::/parent/leaf\n0::/", false},
		{"mount-missing", "/proc/self/mountinfo", "", true},
		{"hidden-ancestors", "/proc/self/mountinfo", "35 25 0:30 /parent /sys/fs/cgroup rw - cgroup2 cgroup2 rw", false},
		{"namespace-hidden-ancestors", "/sys/fs/cgroup/cgroup.type", "domain", false},
		{"cpu-missing", "/sys/fs/cgroup/parent/cpu.max", "", true},
		{"cpu-zero-period", "/sys/fs/cgroup/parent/cpu.max", "1000 0", false},
		{"cpu-negative-quota", "/sys/fs/cgroup/parent/cpu.max", "-1 100000", false},
		{"cpu-incomplete", "/sys/fs/cgroup/parent/cpu.max", "max", false},
		{"cpu-overflow", "/sys/fs/cgroup/parent/cpu.max", "99999999999999999999 100000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := v2HostFixture()
			if tc.remove {
				delete(files, tc.file)
			} else {
				files[tc.file] = tc.value
			}
			stale := float64(100)
			h := Health{EffectiveCPUCores: &stale}
			fillHostResources(&h, hostFixture(t, files), 4)
			if h.EffectiveCPUCores != nil {
				t.Fatalf("unknown CPU limit accepted: %+v", h)
			}
		})
	}
}

func TestHostResourcesV1AndHybrid(t *testing.T) {
	files := map[string]string{
		"/proc/self/cgroup":            "4:cpu,cpuacct:/parent/leaf\n0::/unified\n",
		"/proc/self/mountinfo":         "31 20 0:31 / /cgroup/cpu rw - cgroup cgroup rw,cpu,cpuacct\n",
		"/cgroup/cpu/release_agent":    "",
		"/cgroup/cpu/cpu.cfs_quota_us": "-1", "/cgroup/cpu/cpu.cfs_period_us": "100000",
		"/cgroup/cpu/parent/cpu.cfs_quota_us": "75000", "/cgroup/cpu/parent/cpu.cfs_period_us": "100000",
		"/cgroup/cpu/parent/leaf/cpu.cfs_quota_us": "200000", "/cgroup/cpu/parent/leaf/cpu.cfs_period_us": "100000",
	}
	var h Health
	fillHostResources(&h, hostFixture(t, files), 4)
	if h.EffectiveCPUCores == nil || *h.EffectiveCPUCores != .75 {
		t.Fatalf("v1 inherited limits: %+v", h)
	}
	files["/cgroup/cpu/parent/cpu.cfs_quota_us"], files["/cgroup/cpu/parent/leaf/cpu.cfs_quota_us"] = "-1", "-1"
	fillHostResources(&h, hostFixture(t, files), 4)
	if h.EffectiveCPUCores == nil || *h.EffectiveCPUCores != 4 {
		t.Fatalf("v1 unlimited: %+v", h)
	}
}

func TestHostResourcesMissingAndChangingEvidence(t *testing.T) {
	files := v2HostFixture()
	delete(files, "/proc/meminfo")
	var h Health
	fillHostResources(&h, hostFixture(t, files), 0)
	if h.TotalMemoryBytes != nil || h.AvailableMemoryBytes != nil || h.EffectiveCPUCores != nil {
		t.Fatalf("missing base evidence: %+v", h)
	}
	read := hostFixture(t, v2HostFixture())
	for _, mode := range []string{"permission", "migration"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			fillHostResources(&h, func(name string) ([]byte, error) {
				if mode == "permission" && strings.HasSuffix(name, "cpu.max") {
					return nil, os.ErrPermission
				}
				if name == "/proc/self/cgroup" {
					calls++
					if mode == "migration" && calls > 1 {
						return []byte("0::/new"), nil
					}
				}
				return read(name)
			}, 4)
			if h.EffectiveCPUCores != nil {
				t.Fatalf("unreliable evidence accepted: %+v", h)
			}
		})
	}
}

func TestMeminfoBytes(t *testing.T) {
	for _, value := range []string{"MemTotal: 0 kB", "MemTotal: -1 kB", "MemTotal: 9223372036854775807 kB", "MemTotal: 4 MB", "MemTotal: 4 kB\nMemTotal: 5 kB", "MemTotal: nope kB"} {
		if meminfoBytes(value, "MemTotal:") != nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if n := meminfoBytes("MemAvailable: 0 kB", "MemAvailable:"); n == nil || *n != 0 {
		t.Fatal("zero available is valid")
	}
}

func TestHealthCgroupMountPaths(t *testing.T) {
	g, ok := findHealthCgroup("0::/parent/leaf", `1 2 0:1 / /custom\040mount rw - cgroup2 cgroup2 rw`, "cpu")
	if !ok || g.dir != "/custom mount/parent/leaf" || g.mount != "/custom mount" {
		t.Fatalf("escaped mount: %+v %v", g, ok)
	}
	g, ok = findHealthCgroup("5:pids:/leaf", "", "cpu")
	if !ok || g.dir != "" {
		t.Fatalf("absent controller: %+v %v", g, ok)
	}
}

func TestHostHealthReadOnly(t *testing.T) {
	dir := t.TempDir()
	sampler := new(hostHealthSampler)
	a := agent{config: AgentConfig{StateDirectory: dir, Probe: probe}}
	before := time.Now().UTC()
	h, err := a.health(context.Background(), sampler)
	if err != nil || h.ObservedAt.Before(before) || h.ObservedAt.After(time.Now()) || h.ObservedAt.Location() != time.UTC {
		t.Fatalf("invalid sample identity: %+v, %v", h, err)
	}
	if h.CPUUtilization != nil || h.TotalMemoryBytes == nil || *h.TotalMemoryBytes <= 0 || h.AvailableMemoryBytes == nil || *h.AvailableMemoryBytes < 0 || h.CPUCount == nil || *h.CPUCount <= 0 {
		t.Fatalf("invalid initial host observation: %+v", h)
	}
	var disk syscall.Statfs_t
	if err := syscall.Statfs(dir, &disk); err != nil {
		t.Fatal(err)
	}
	if h.AvailableDiskBytes == nil || *h.AvailableDiskBytes < 0 || *h.AvailableDiskBytes > int64(disk.Blocks)*disk.Bsize {
		t.Fatal("state filesystem free space unavailable or invalid")
	}
	if os.Getenv("OAC_TEST_NODE_HEALTH_PROBE") == "1" {
		data, err := json.Marshal(h)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(string(data))
	}
	time.Sleep(100 * time.Millisecond)
	h, err = a.health(context.Background(), sampler)
	if err != nil || h.CPUUtilization == nil || *h.CPUUtilization < 0 || *h.CPUUtilization > 1 || !h.ObservedAt.After(before) {
		t.Fatalf("invalid follow-up observation: %+v, %v", h, err)
	}
	if os.Getenv("OAC_TEST_NODE_HEALTH_PROBE") == "1" {
		data, _ := json.Marshal(h)
		t.Log(string(data))
	}
	if fresh, _ := a.health(context.Background(), new(hostHealthSampler)); fresh.CPUUtilization != nil {
		t.Fatal("new connection reused a CPU baseline")
	}
	sampler.fill(&h, filepath.Join(dir, "missing"))
	if h.AvailableDiskBytes != nil {
		t.Fatal("failed state filesystem must not use another disk")
	}
}

func TestHealthResourcesJSONNull(t *testing.T) {
	data, err := json.Marshal(Health{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"total_memory_bytes", "cpu_utilization", "effective_cpu_cores"} {
		if string(fields[key]) != "null" {
			t.Fatalf("unknown %s = %s", key, fields[key])
		}
	}
}

func TestHealthReadBounds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(file, []byte(strings.Repeat("x", healthMaxFileBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHealthFile(file); err == nil {
		t.Fatal("oversized observation accepted")
	}
	g := healthCgroup{dir: "/cgroup" + strings.Repeat("/child", healthMaxCgroupDepth), mount: "/cgroup", v2: true}
	reads := 0
	read := func(name string) ([]byte, error) {
		reads++
		switch filepath.Base(name) {
		case "cgroup.type":
			return nil, os.ErrNotExist
		case "cpu.max":
			return []byte("max 100000"), nil
		default:
			t.Fatalf("unexpected read: %s", name)
			return nil, os.ErrNotExist
		}
	}
	if _, ok := g.cpuLimit(read); ok {
		t.Fatal("incomplete CPU ancestry accepted")
	}
	if reads > (healthMaxCgroupDepth + 1) {
		t.Fatalf("unbounded ancestry reads: %d", reads)
	}
}
