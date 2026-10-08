package mcode

import (
	"strings"
	"testing"
)

func TestValidateWorkspaceReadinessRejectsInvalidDescriptors(t *testing.T) {
	if err := ValidateWorkspaceReadiness([]byte(`{"protocol":2,"native":"0.4.12","source":"33b259bbbeb1c16433390869938191d09bdb0680"}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "null", "{", strings.Repeat(" ", 4097),
		`{"protocol":1,"native":"0.4.12","source":"33b259bbbeb1c16433390869938191d09bdb0680"}`,
		`{"protocol":2,"native":"0.4.11","source":"33b259bbbeb1c16433390869938191d09bdb0680"}`,
		`{"protocol":2,"native":"0.4.12","source":"other"}`} {
		if err := ValidateWorkspaceReadiness([]byte(raw)); err == nil || err.Error() != "mcode: workspace companion check failed" {
			t.Fatal("invalid descriptor accepted or exposed", err)
		}
	}
}
