package coremetrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessLinuxParsers(t *testing.T) {
	for _, data := range []string{"", "VmRSS: x kB", "VmRSS: -1 kB", "VmRSS: 10 MB", "VmRSS: 18446744073709551615 kB", "VmRSS: 10 kB extra"} {
		if parseRSS(data) != nil {
			t.Fatal("invalid RSS accepted", data)
		}
	}
	if got := parseRSS("Name: core\nVmRSS:\t123 kB\n"); got == nil || *got != 123*1024 {
		t.Fatal(got)
	}
	if got := parseRSS("VmRSS: 0 kB"); got == nil || *got != 0 {
		t.Fatal("measured zero RSS lost")
	}
	for _, data := range []string{"", "max", "0 100000", "1000 0", "-1 1000", "1.5 1000", "max nope", "1 1000 extra", "18446744073709551616 1000"} {
		if parseCPULimit(data, 4) != nil {
			t.Fatal("invalid CPU limit accepted", data)
		}
	}
	if got := parseCPULimit("150000 100000\n", 4); got == nil || *got != 1.5 {
		t.Fatal(got)
	}
	if got := parseCPULimit("max 100000\n", 4); got == nil || *got != 4 {
		t.Fatal(got)
	}
	for _, data := range []string{"", "max", "-1", "2.5", "18446744073709551616", "1024 extra"} {
		if parseMemoryLimit(data) != nil {
			t.Fatal("invalid memory limit accepted", data)
		}
	}
	if got := parseMemoryLimit("1073741824\n"); got == nil || *got != 1073741824 {
		t.Fatal(got)
	}
}

func TestProcessCgroupDirectory(t *testing.T) {
	for _, test := range []struct{ name, membership, mountinfo, want string }{
		{"nested", "0::/system.slice/core.service\n", "1 0 0:1 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", "/sys/fs/cgroup/system.slice/core.service"},
		{"namespace root", "0::/\n", "1 0 0:1 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", "/sys/fs/cgroup"},
		{"subtree", "0::/work/core\n", "1 0 0:1 /work /run/cgroup rw - cgroup2 cgroup rw\n", "/run/cgroup/core"},
		{"exact subtree", "0::/work\n", "1 0 0:1 /work /run/cgroup rw - cgroup2 cgroup rw\n", "/run/cgroup"},
		{"escaped mount", "0::/work/core\n", "1 0 0:1 /work /run/cgroup\\040space rw - cgroup2 cgroup rw\n", "/run/cgroup space/core"},
		{"unrelated subtree", "0::/workers/core\n", "1 0 0:1 /work /run/cgroup rw - cgroup2 cgroup rw\n", ""},
		{"v1", "1:cpu:/work\n", "1 0 0:1 / /sys/fs/cgroup rw - cgroup cgroup rw\n", ""},
		{"invalid membership", "0::/work/../core\n", "1 0 0:1 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", ""},
		{"invalid mount", "0::/work\n", "1 0 0:1 / relative rw - cgroup2 cgroup rw\n", ""},
		{"duplicate membership", "0::/one\n0::/two\n", "1 0 0:1 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", ""},
		{"specific mount", "0::/work/core\n", "1 0 0:1 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n2 0 0:1 /work /run/cgroup rw - cgroup2 cgroup rw\n", "/run/cgroup/core"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, _ := cgroupDirectory(test.membership, test.mountinfo); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestProcessLinuxFiles(t *testing.T) {
	dir := t.TempDir()
	group := filepath.Join(dir, "cgroup", "service")
	if err := os.MkdirAll(group, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// proc and cgroup mounts have independent roots.
	proc := filepath.Join(dir, "proc")
	if err := os.Mkdir(proc, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(proc, "status"), "VmRSS: 123 kB\n")
	write(filepath.Join(proc, "cgroup"), "0::/tenant/service\n")
	write(filepath.Join(proc, "mountinfo"), "1 0 0:1 /tenant "+filepath.Dir(group)+" rw - cgroup2 cgroup rw\n")
	write(filepath.Join(group, "cpu.max"), "150000 100000\n")
	write(filepath.Join(group, "memory.max"), "1073741824\n")
	got := readProcessFiles(proc, 4)
	if got.rssBytes == nil || *got.rssBytes != 123*1024 || got.cpuLimitCores == nil || *got.cpuLimitCores != 1.5 || got.memoryLimitBytes == nil || *got.memoryLimitBytes != 1073741824 {
		t.Fatal(got)
	}
	write(filepath.Join(group, "cpu.max"), "max 100000\n")
	write(filepath.Join(group, "memory.max"), "max\n")
	got = readProcessFiles(proc, 4)
	if got.cpuLimitCores == nil || *got.cpuLimitCores != 4 || got.memoryLimitBytes != nil {
		t.Fatal(got)
	}
	if err := os.Remove(filepath.Join(group, "cpu.max")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(proc, "status"), strings.Repeat("x", 64<<10)+"\nVmRSS: 1 kB\n")
	got = readProcessFiles(proc, 4)
	if got.cpuLimitCores != nil || got.rssBytes != nil {
		t.Fatal("missing or oversized reads must be unknown", got)
	}
}

func TestProcessRootCgroupWithoutQuota(t *testing.T) {
	dir := t.TempDir()
	group := filepath.Join(dir, "cgroup")
	if err := os.Mkdir(group, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	proc := filepath.Join(dir, "proc")
	if err := os.Mkdir(proc, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(proc, "cgroup"), "0::/\n")
	write(filepath.Join(proc, "mountinfo"), "1 0 0:1 / "+group+" rw - cgroup2 cgroup rw\n")
	write(filepath.Join(group, "cgroup.controllers"), "cpu memory\n")
	got := readProcessFiles(proc, 3)
	if got.cpuLimitCores == nil || *got.cpuLimitCores != 3 || got.memoryLimitBytes != nil {
		t.Fatal("real root must use GOMAXPROCS", got)
	}
	write(filepath.Join(group, "cgroup.type"), "domain\n")
	if got := readProcessFiles(proc, 3); got.cpuLimitCores != nil {
		t.Fatal("namespace root missing quota must stay unknown", got)
	}
	if err := os.Remove(filepath.Join(group, "cgroup.type")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(group, "cpu.max"), "invalid\n")
	if got := readProcessFiles(proc, 3); got.cpuLimitCores != nil {
		t.Fatal("invalid quota became unlimited", got)
	}
	if err := os.Remove(filepath.Join(group, "cpu.max")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(group, "cpu.max"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := readProcessFiles(proc, 3); got.cpuLimitCores != nil {
		t.Fatal("failed read became unlimited", got)
	}
}
