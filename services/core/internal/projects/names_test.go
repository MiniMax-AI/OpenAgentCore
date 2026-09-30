package projects

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNameValidationIdentityAndBoundaries(t *testing.T) {
	for _, max := range []int{KeyNameMaxLength, ProjectNameMaxLength} {
		for _, name := range []string{"", " ", "private-name\t", strings.Repeat("a", max+1), string([]byte{0xff})} {
			_, err := normalizeName(name, max)
			var field *NameError
			if !errors.As(err, &field) || field.MaxLength != max || err.Error() != fmt.Sprintf("name must contain 1–%d characters without controls", max) {
				t.Fatalf("wrong name error: %#v", err)
			}
		}
		if got, err := normalizeName(" "+strings.Repeat("界", max)+" ", max); err != nil || got != strings.Repeat("界", max) {
			t.Fatal("rune/trim semantics changed", err)
		}
	}
}

func TestListQueryPageSize(t *testing.T) {
	for _, limit := range []int{-1, 0, MaxListLimit + 1} {
		if err := (ListQuery{Limit: limit}).Validate(); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("page size accepted", limit, err)
		}
	}
	for _, limit := range []int{1, MaxListLimit} {
		if err := (ListQuery{Limit: limit}).Validate(); err != nil {
			t.Fatal("page size rejected", limit, err)
		}
	}
}
