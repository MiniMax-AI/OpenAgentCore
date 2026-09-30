//go:build !linux

package coremetrics

import "runtime"

func readProcess() processReading {
	return processReading{cpuLimitCores: ptr(float64(runtime.GOMAXPROCS(0)))}
}
