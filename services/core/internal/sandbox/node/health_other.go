//go:build !linux

package node

type hostHealthSampler struct{}

func (*hostHealthSampler) fill(h *Health, _ string) {
	h.CPUUtilization, h.TotalMemoryBytes, h.EffectiveCPUCores = nil, nil, nil
}
