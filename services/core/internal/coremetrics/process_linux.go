//go:build linux

package coremetrics

import (
	"runtime"
	"syscall"
)

func readProcess() processReading {
	reading := readProcessFiles("/proc/self", runtime.GOMAXPROCS(0))
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) == nil {
		reading.cpuSeconds = ptr(float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6 + float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6)
	}
	return reading
}
