package pgunit

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUnstorableText(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&pgconn.PgError{Code: "22021"}, true},
		{fmt.Errorf("insert: %w", &pgconn.PgError{Code: "22P05"}), true},
		{&pgconn.PgError{Code: "23505"}, false},
		{errors.New("22021"), false},
		{nil, false},
	} {
		if got := IsUnstorableText(tc.err); got != tc.want {
			t.Errorf("IsUnstorableText(%v) = %v", tc.err, got)
		}
	}
}
