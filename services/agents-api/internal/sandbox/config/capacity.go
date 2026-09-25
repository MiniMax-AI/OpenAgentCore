package config

import (
	"fmt"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

func checkCapacity(r sandbox.Resources, cpus int, memory uint64) error {
	if cpus < int(r.CPUs) || memory < uint64(r.MemoryMiB)*1024*1024 {
		return fmt.Errorf("%w: one sandbox requires %d CPUs and %d MiB memory; available host capacity is %d CPUs and %d MiB", sandbox.ErrCapacityInsufficient, r.CPUs, r.MemoryMiB, cpus, memory/1024/1024)
	}
	return nil
}
