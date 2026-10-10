//go:build linux || darwin

package localworkspace

import (
	"errors"
	"syscall"
)

func storageExhausted(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}
