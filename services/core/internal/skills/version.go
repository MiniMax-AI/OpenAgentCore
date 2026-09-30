package skills

import (
	"errors"
	"strconv"
)

// ErrInvalidVersion reports a version that is not a canonical positive decimal.
var ErrInvalidVersion = errors.New("skill version must be a canonical positive decimal integer")

// ParseVersion accepts only the canonical decimal spelling of a positive
// version number, so every version has exactly one text form. Selecting
// "latest" or an omitted version belongs to the caller.
func ParseVersion(value string) (int64, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 1 || strconv.FormatInt(number, 10) != value {
		return 0, ErrInvalidVersion
	}
	return number, nil
}
