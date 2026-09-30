package e2b

import (
	"encoding/json"
	"os"
	"testing"
)

// The installer consumes this same fixture; native selectors are owned here.
func TestSharedConfigurationSelectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/configuration-selectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Endpoints []struct {
			Name   string
			APIURL string `json:"api_url"`
			Domain string
			Valid  bool
		}
		Templates []struct {
			Name  string
			Value string
			Valid bool
		}
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases.Endpoints {
		t.Run("endpoint/"+tc.Name, func(t *testing.T) {
			apiURL, domain, err := NormalizeEndpoint(tc.APIURL, tc.Domain)
			if (err == nil) != tc.Valid {
				t.Fatalf("valid=%v: %v", tc.Valid, err)
			}
			if tc.Valid {
				expectedURL, expectedDomain := tc.APIURL, tc.Domain
				if expectedURL == "" && expectedDomain == "" {
					expectedURL, expectedDomain = OfficialAPIURL, OfficialDomain
				}
				if apiURL != expectedURL || domain != expectedDomain {
					t.Fatalf("unexpected normalized selectors: %q %q", apiURL, domain)
				}
			}
		})
	}
	for _, tc := range cases.Templates {
		t.Run("template/"+tc.Name, func(t *testing.T) {
			err := ValidateConfiguration(&DeploymentConfiguration{APIKey: "fixture-key", Template: tc.Value})
			if (err == nil) != tc.Valid {
				t.Fatalf("valid=%v: %v", tc.Valid, err)
			}
		})
	}
}
