package processconfig

import (
	"strings"
	"testing"
)

func TestSandboxCapacityDefaultsAndSettings(t *testing.T) {
	t.Setenv("OAC_SANDBOX_MAX_ACTIVE", "")
	t.Setenv("OAC_SANDBOX_MAX_RETAINED", "")
	capacity, err := SandboxCapacity()
	if err != nil || capacity.MaxActive != 100 || capacity.MaxRetained != 400 {
		t.Fatal("empty Compose settings lost defaults", capacity, err)
	}
	t.Setenv("OAC_SANDBOX_MAX_ACTIVE", "7")
	t.Setenv("OAC_SANDBOX_MAX_RETAINED", "31")
	settings, err := Settings()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]any{}
	for _, setting := range settings {
		found[setting.Key] = setting.Value
	}
	if found["core.sandbox_capacity.max_active"] != 7 || found["core.sandbox_capacity.max_retained"] != 31 {
		t.Fatal("installation omitted effective direct capacity", found)
	}
}

func TestCheckValidatesSandboxCapacity(t *testing.T) {
	for _, key := range []string{"OAC_SANDBOX_MAX_ACTIVE", "OAC_SANDBOX_MAX_RETAINED"} {
		for _, tc := range []struct {
			value string
			valid bool
		}{{"0", false}, {"-1", false}, {"1.5", false}, {"synthetic-secret-value", false}, {"100001", false}, {"100000", true}, {"1", true}, {"", true}} {
			t.Run(key+"/"+tc.value, func(t *testing.T) {
				t.Setenv("OAC_SANDBOX_MAX_ACTIVE", "1")
				t.Setenv("OAC_SANDBOX_MAX_RETAINED", "100000")
				t.Setenv(key, tc.value)
				err := Check()
				if (err == nil) != tc.valid || err != nil && (!strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "synthetic-secret-value")) {
					t.Fatal("wrong process validation", err)
				}
			})
		}
	}
	t.Setenv("OAC_SANDBOX_MAX_ACTIVE", "100")
	t.Setenv("OAC_SANDBOX_MAX_RETAINED", "99")
	if err := Check(); err == nil {
		t.Fatal("accepted retained limit below active limit")
	}
}
