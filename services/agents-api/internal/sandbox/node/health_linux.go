//go:build linux

package node

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func fillHostHealth(h *Health, dir string) {
	cpu := int64(runtime.NumCPU())
	h.CPUCount = &cpu
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) == 3 && fields[0] == "MemAvailable:" && fields[2] == "kB" {
				if n, e := strconv.ParseInt(fields[1], 10, 64); e == nil && n >= 0 && n < (1<<63-1)/1024 {
					n *= 1024
					h.AvailableMemoryBytes = &n
				}
				break
			}
		}
	}
	var disk syscall.Statfs_t
	if syscall.Statfs(dir, &disk) == nil && disk.Bsize > 0 && disk.Bavail < uint64((1<<63-1)/disk.Bsize) {
		available := int64(disk.Bavail) * disk.Bsize
		h.AvailableDiskBytes = &available
	}
}
