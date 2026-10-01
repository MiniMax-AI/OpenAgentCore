//go:build !linux

package sessionview

import "time"

// EndLeftoverViews reports that views need Linux.
func EndLeftoverViews(time.Duration) error { return ErrUnsupported }
