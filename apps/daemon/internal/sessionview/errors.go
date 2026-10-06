package sessionview

import (
	"errors"
	"fmt"
	"strings"
)

// Error kinds. Every error the package returns matches one of them with errors.Is.
var (
	ErrInvalidSpec = errors.New("sessionview: invalid spec")
	ErrCapability  = errors.New("sessionview: missing capability")
	ErrNoFUSE      = errors.New("sessionview: /dev/fuse unavailable")
	ErrNoSeccomp   = errors.New("sessionview: seccomp filter unavailable")
	ErrMountDenied = errors.New("sessionview: mount denied")
	ErrMountTarget = errors.New("sessionview: invalid mount target")
	ErrWorld       = errors.New("sessionview: world server failed")
	ErrNetwork     = errors.New("sessionview: network setup failed")
	ErrRestrict    = errors.New("sessionview: privilege drop failed")
	ErrExec        = errors.New("sessionview: process start failed")
	ErrLauncher    = errors.New("sessionview: launcher failed")
	ErrClosed      = errors.New("sessionview: view closed")
	// ErrCgroup is a cgroup parent that fails Recover's checks, or a view cgroup that could not be created.
	ErrCgroup = errors.New("sessionview: view cgroup unavailable")
	// ErrCleanup reports a teardown or recovery that did not finish within its bound, or a cgroup that could not be ended and removed. The cgroup stays for Recover, and what remains finishes in the background if it can.
	ErrCleanup = errors.New("sessionview: cleanup incomplete")
	// ErrExited is Signal's and Spawn's result once the process has exited. Signal's also matches os.ErrProcessDone.
	ErrExited = errors.New("sessionview: process exited")
)

// errorKinds fixes the wire code of each kind the launcher reports.
var errorKinds = []error{ErrLauncher, ErrNoFUSE, ErrMountDenied, ErrMountTarget, ErrNetwork, ErrRestrict, ErrExec, ErrExited}

// Error is a typed sessionview failure. It matches Kind and, when present, Err.
type Error struct {
	Kind error
	Op   string
	Path string
	Err  error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Kind.Error())
	if e.Op != "" {
		b.WriteString(": " + e.Op)
	}
	if e.Path != "" {
		b.WriteString(" " + e.Path)
	}
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

func (e *Error) Unwrap() []error {
	if e.Err == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Err}
}

func invalid(format string, args ...any) error {
	return &Error{Kind: ErrInvalidSpec, Err: fmt.Errorf(format, args...)}
}
