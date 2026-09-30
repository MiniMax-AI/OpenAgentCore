//go:build linux

package sessionview

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// requiredCaps are what the launcher uses: namespaces and mounts, chroot, loopback, the process's credentials, dropping the bounding set and signalling the process.
var requiredCaps = []struct {
	bit  int
	name string
}{
	{unix.CAP_SYS_ADMIN, "CAP_SYS_ADMIN"},
	{unix.CAP_SYS_CHROOT, "CAP_SYS_CHROOT"},
	{unix.CAP_NET_ADMIN, "CAP_NET_ADMIN"},
	{unix.CAP_SETUID, "CAP_SETUID"},
	{unix.CAP_SETGID, "CAP_SETGID"},
	{unix.CAP_SETPCAP, "CAP_SETPCAP"},
	{unix.CAP_KILL, "CAP_KILL"},
}

var probe = sync.OnceValue(probeHost)

// Probe checks once that this host can build views and returns the same typed error afterwards.
func Probe() error {
	return probe()
}

func probeHost() error {
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return &Error{Kind: ErrCapability, Op: "capget", Err: err}
	}
	effective := uint64(data[1].Effective)<<32 | uint64(data[0].Effective)
	var missing []string
	for _, c := range requiredCaps {
		if effective&(1<<c.bit) == 0 {
			missing = append(missing, c.name)
		}
	}
	if len(missing) > 0 {
		return &Error{Kind: ErrCapability, Op: "probe", Err: fmt.Errorf("missing %s", strings.Join(missing, ", "))}
	}
	dev, err := unix.Open("/dev/fuse", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return &Error{Kind: ErrNoFUSE, Op: "open", Path: "/dev/fuse", Err: err}
	}
	unix.Close(dev)
	if err := onThrowawayThread(probeMount); err != nil {
		return err
	}
	return onThrowawayThread(probeSeccomp)
}

// onThrowawayThread runs f on a dedicated thread that is never unlocked, so the thread exits with the goroutine and takes any namespace or filter f sets up with it.
func onThrowawayThread(f func() error) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		errc <- f()
	}()
	return <-errc
}

// probeMount mounts a tmpfs in a private mount namespace.
func probeMount() error {
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return mountProbeError("unshare", "", err)
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return mountProbeError("make-rprivate", "/", err)
	}
	dir, err := os.MkdirTemp("", "oac-view-probe-*")
	if err != nil {
		return &Error{Kind: ErrLauncher, Op: "probe", Err: err}
	}
	defer os.Remove(dir)
	if err := unix.Mount("oac-probe", dir, "tmpfs", fuseMountFlags, "size=4k"); err != nil {
		return mountProbeError("mount", dir, err)
	}
	if err := unix.Unmount(dir, 0); err != nil {
		return mountProbeError("umount", dir, err)
	}
	return nil
}

// probeSeccomp installs the view's seccomp filter.
func probeSeccomp() error {
	return installSeccomp(ErrNoSeccomp)
}

func mountProbeError(op, path string, err error) error {
	return &Error{Kind: ErrMountDenied, Op: op, Path: path, Err: err}
}
