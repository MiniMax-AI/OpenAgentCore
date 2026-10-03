package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func testSandboxCapacity(t testing.TB) sandboxCapacity {
	t.Helper()
	c, err := configuredSandboxCapacity()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSandboxCapacityMatchesInstallationSchema(t *testing.T) {
	for _, name := range []string{"OAC_SANDBOX_MAX_ACTIVE", "OAC_SANDBOX_MAX_RETAINED"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile("../../../../deploy/install/config.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Core struct {
				Properties struct {
					Capacity struct {
						Properties map[string]struct {
							Default int `json:"default"`
							Minimum int `json:"minimum"`
							Maximum int `json:"maximum"`
						} `json:"properties"`
					} `json:"sandbox_capacity"`
				} `json:"properties"`
			} `json:"core"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema.Properties.Core.Properties.Capacity.Properties
	c := testSandboxCapacity(t)
	if c.MaxActive != properties["max_active"].Default || c.MaxRetained != properties["max_retained"].Default {
		t.Fatal("Core defaults drifted from the installation schema")
	}
	for _, field := range []struct{ key, env string }{{"max_active", "OAC_SANDBOX_MAX_ACTIVE"}, {"max_retained", "OAC_SANDBOX_MAX_RETAINED"}} {
		p := properties[field.key]
		for _, tc := range []struct {
			value string
			valid bool
		}{{"0", false}, {"-1", false}, {"1.5", false}, {"", false}, {"secret-value", false}, {"100001", false}, {"100000", true}, {"1", true}} {
			t.Setenv("OAC_SANDBOX_MAX_ACTIVE", "1")
			t.Setenv("OAC_SANDBOX_MAX_RETAINED", "100000")
			t.Setenv(field.env, tc.value)
			_, err := configuredSandboxCapacity()
			if (err == nil) != tc.valid || err != nil && strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("%s: wrong validation for %q: %v", field.key, tc.value, err)
			}
		}
		if p.Minimum != 1 || p.Maximum != 100000 {
			t.Fatal("Core bounds drifted from the installation schema")
		}
	}
	t.Setenv("OAC_SANDBOX_MAX_ACTIVE", "100")
	t.Setenv("OAC_SANDBOX_MAX_RETAINED", "99")
	if _, err := configuredSandboxCapacity(); err == nil {
		t.Fatal("accepted retained capacity below active capacity")
	}
}
