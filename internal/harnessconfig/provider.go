package harnessconfig

func (c Configuration) AcceptsHarnessConfig() bool { return c.ValidateNativeConfig != nil }
