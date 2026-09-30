//go:build linux

package sessionview

import (
	"math"

	"golang.org/x/sys/unix"
)

// restrict leaves the calling thread, which forks the process, with only what the process may inherit: no fds beyond 0–2, empty bounding, ambient and inheritable capability sets, no_new_privs and the seccomp filter. The child's setuid clears the remaining effective and permitted sets.
func restrict() error {
	if err := unix.CloseRange(3, math.MaxUint32, unix.CLOSE_RANGE_CLOEXEC); err != nil {
		return &Error{Kind: ErrRestrict, Op: "close_range", Err: err}
	}
	if err := dropCapabilities(); err != nil {
		return err
	}
	return installSeccomp(ErrRestrict)
}

func dropCapabilities() error {
	for c := 0; c < 64; c++ {
		err := unix.Prctl(unix.PR_CAPBSET_DROP, uintptr(c), 0, 0, 0)
		if err == unix.EINVAL {
			break
		}
		if err != nil {
			return &Error{Kind: ErrRestrict, Op: "capbset_drop", Err: err}
		}
	}
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return &Error{Kind: ErrRestrict, Op: "cap_ambient_clear_all", Err: err}
	}
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return &Error{Kind: ErrRestrict, Op: "capget", Err: err}
	}
	data[0].Inheritable, data[1].Inheritable = 0, 0
	if err := unix.Capset(&hdr, &data[0]); err != nil {
		return &Error{Kind: ErrRestrict, Op: "capset", Err: err}
	}
	return nil
}
