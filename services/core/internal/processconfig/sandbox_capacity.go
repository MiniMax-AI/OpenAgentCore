package processconfig

import (
	"os"
	"strconv"
)

const defaultSandboxMaxActive = 100
const defaultSandboxMaxRetained = 400

// SandboxLimits bounds direct Providers with suspension independently of
// execution concurrency and each enrolled node's capacity.
type SandboxLimits struct {
	MaxActive   int
	MaxRetained int
}

// SandboxCapacity reads the process-owned active and retained limits. Unset or
// empty variables select the same defaults as the other process settings.
func SandboxCapacity() (SandboxLimits, error) {
	capacity := SandboxLimits{MaxActive: defaultSandboxMaxActive, MaxRetained: defaultSandboxMaxRetained}
	for _, setting := range []struct {
		name   string
		target *int
	}{
		{"OAC_SANDBOX_MAX_ACTIVE", &capacity.MaxActive},
		{"OAC_SANDBOX_MAX_RETAINED", &capacity.MaxRetained},
	} {
		if raw := os.Getenv(setting.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 100000 {
				return SandboxLimits{}, configErr(setting.name + " must be an integer between 1 and 100000")
			}
			*setting.target = value
		}
	}
	if capacity.MaxRetained < capacity.MaxActive {
		return SandboxLimits{}, configErr("OAC_SANDBOX_MAX_RETAINED must be at least OAC_SANDBOX_MAX_ACTIVE")
	}
	return capacity, nil
}
