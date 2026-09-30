//go:build linux

package node

import (
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const healthMaxFileBytes = 1024 * 1024

func readHealthFile(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, healthMaxFileBytes+1))
	if len(data) > healthMaxFileBytes {
		return nil, fmt.Errorf("health observation exceeds size limit")
	}
	return data, err
}

type hostHealthSampler struct {
	cpu hostCPUSampler
}

func (s *hostHealthSampler) fill(h *Health, dir string) {
	cpu := int64(runtime.NumCPU())
	h.CPUCount = &cpu
	h.CPUUtilization = s.cpu.sample(readHealthFile)
	// The kernel applies cpuset restrictions and online CPU changes to affinity.
	// Use the process leader, not whichever Go thread happens to run this probe.
	var affinity unix.CPUSet
	cpus := 0
	if unix.SchedGetaffinity(os.Getpid(), &affinity) == nil {
		cpus = affinity.Count()
	}
	fillHostResources(h, readHealthFile, cpus)
	h.AvailableDiskBytes = nil
	var disk syscall.Statfs_t
	if syscall.Statfs(dir, &disk) == nil && disk.Bsize > 0 && disk.Bavail <= uint64(math.MaxInt64/disk.Bsize) {
		available := int64(disk.Bavail) * disk.Bsize
		h.AvailableDiskBytes = &available
	}
}

func fillHostResources(h *Health, readFile func(string) ([]byte, error), cpus int) {
	h.AvailableMemoryBytes, h.TotalMemoryBytes, h.EffectiveCPUCores = nil, nil, nil
	if data, err := readFile("/proc/meminfo"); err == nil {
		h.TotalMemoryBytes = meminfoBytes(string(data), "MemTotal:")
		h.AvailableMemoryBytes = meminfoBytes(string(data), "MemAvailable:")
	}
	groups, err := readFile("/proc/self/cgroup")
	if err != nil {
		return
	}
	mounts, err := readFile("/proc/self/mountinfo")
	if err != nil {
		return
	}
	cpuGroup, cpuOK := findHealthCgroup(string(groups), string(mounts), "cpu")
	if cpuOK && cpus > 0 {
		if limit, ok := cpuGroup.cpuLimit(readFile); ok {
			n := min(float64(cpus), limit)
			h.EffectiveCPUCores = &n
		}
	}
	// A migration during collection cannot establish one effective boundary.
	if after, err := readFile("/proc/self/cgroup"); err != nil || string(after) != string(groups) {
		h.EffectiveCPUCores = nil
	}
}

func meminfoBytes(data, name string) *int64 {
	var result *int64
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != name {
			continue
		}
		if result != nil || len(fields) != 3 || fields[2] != "kB" {
			return nil
		}
		n, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || n < 0 || n > math.MaxInt64/1024 || name == "MemTotal:" && n == 0 {
			return nil
		}
		n *= 1024
		result = &n
	}
	return result
}
