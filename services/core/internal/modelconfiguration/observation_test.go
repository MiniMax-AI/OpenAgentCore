package modelconfiguration

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/observation_cases.json is shared with the adapter's tests, which run
// the same cases through the observation statement.
func TestShouldObserveProvider(t *testing.T) {
	raw, err := os.ReadFile("testdata/observation_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Status          string `json:"status"`
		ErrorCode       string `json:"error_code"`
		EngineErrorCode string `json:"engine_error_code"`
		Observed        bool   `json:"observed"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) == 0 {
		t.Fatal("observation cases unreadable", err)
	}
	for _, tc := range cases {
		if got := ShouldObserveProvider(tc.Status, tc.ErrorCode, tc.EngineErrorCode); got != tc.Observed {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}
