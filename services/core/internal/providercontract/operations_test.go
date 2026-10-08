package providercontract

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestObservationReasonFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/observation_reasons.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Reason string
			Valid  bool
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, entry := range fixture.Cases {
		t.Run(entry.Reason, func(t *testing.T) {
			err := (Support{State: Unsupported, Reason: entry.Reason}).Check("Observe")
			if errors.Is(err, ErrUnsupported) != entry.Valid || errors.Is(err, ErrContract) == entry.Valid {
				t.Fatalf("declaration result = %v, valid = %t", err, entry.Valid)
			}
			reason, valid := UnsupportedReason(&UnsupportedError{Operation: "Observe", Reason: entry.Reason}, "Observe")
			if valid != entry.Valid || valid && reason != entry.Reason {
				t.Fatalf("unsupported result = %q, %t", reason, valid)
			}
		})
	}
}
