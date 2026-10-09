//go:build linux

package sessionview

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
)

// builder mounts the local pieces onto the world, at the paths the world presents the mountpoints at. Every target is resolved beneath its parent mount without following symlinks, so the sandbox cannot redirect a mount.
type builder struct {
	root    int               // the world root, an O_PATH fd
	targets map[string]string // each mountpoint's view path to where the world presents it
	fds     []int             // what only the build needs

	// What the launcher keeps: the view's /proc, and the relay's listening socket or -1.
	proc, listener int
}

// devNodes are bound from the host's /dev into the view's /dev.
var devNodes = []string{"null", "zero", "full", "random", "urandom", "tty"}

var devLinks = [][2]string{
	{"ptmx", "pts/ptmx"},
	{"fd", "/proc/self/fd"},
	{"stdin", "/proc/self/fd/0"},
	{"stdout", "/proc/self/fd/1"},
	{"stderr", "/proc/self/fd/2"},
}

const (
	attrNoSuid = unix.MOUNT_ATTR_NOSUID
	attrNoDev  = unix.MOUNT_ATTR_NODEV
	attrNoExec = unix.MOUNT_ATTR_NOEXEC
)

func (b *builder) build(spec *launchSpec) error {
	root, err := unix.Open(spec.Staging, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return &Error{Kind: ErrLauncher, Op: "open", Path: spec.Staging, Err: err}
	}
	b.root = b.keep(root)
	for _, d := range spec.Private {
		at, err := b.at(agent.ViewPrivateRoot + "/" + d.Name)
		if err != nil {
			return err
		}
		src, err := b.source(d.HostDir)
		if err != nil {
			return err
		}
		if err := b.bind(src, b.root, at, bindAttr(d.Writable, d.Exec, false)); err != nil {
			return err
		}
	}
	shim, names := -1, spec.Shim.Names
	if spec.Shim.declared() {
		if shim, err = b.source(spec.Shim.Binary); err != nil {
			return err
		}
		names = append(slices.Clone(names), processshim.RelayName)
	}
	if err := b.shimDir(names, shim); err != nil {
		return err
	}
	if spec.Shim.declared() {
		if err := b.runDir(spec.UID, spec.GID); err != nil {
			return err
		}
	}
	for _, o := range spec.Overlays {
		at, err := b.at(o.Path)
		if err != nil {
			return err
		}
		src, err := b.source(o.Source)
		if err != nil {
			return err
		}
		if err := b.bind(src, b.root, at, bindAttr(false, o.Exec, false)); err != nil {
			return err
		}
	}
	for _, p := range spec.Shim.Paths {
		at, err := b.at(p)
		if err != nil {
			return err
		}
		if err := b.bind(shim, b.root, at, bindAttr(false, true, false)); err != nil {
			return err
		}
	}
	at, err := b.at(agent.ViewProcRoot)
	if err != nil {
		return err
	}
	if b.proc, err = newFS("proc", nil, attrNoSuid|attrNoDev|attrNoExec); err != nil {
		return err
	}
	if err := b.attach(b.proc, b.root, at, true); err != nil {
		return err
	}
	at, err = b.at(agent.ViewSysRoot)
	if err != nil {
		return err
	}
	sys, err := newFS("sysfs", nil, unix.MOUNT_ATTR_RDONLY|attrNoSuid|attrNoDev|attrNoExec)
	if err != nil {
		return err
	}
	defer unix.Close(sys)
	if err := b.attach(sys, b.root, at, true); err != nil {
		return err
	}
	// Mount in the view's cgroup namespace, never bind the host hierarchy.
	cgroup, err := newFS("cgroup2", nil, unix.MOUNT_ATTR_RDONLY|attrNoSuid|attrNoDev|attrNoExec)
	if err != nil {
		return err
	}
	defer unix.Close(cgroup)
	if err := b.attachAt(cgroup, sys, "fs/cgroup", agent.ViewSysRoot+"/fs/cgroup", true); err != nil {
		return err
	}
	return b.dev()
}

// emptyRoot mounts at staging a read-only, noexec tmpfs that holds only the mountpoints and their parent directories.
func emptyRoot(staging string, mps []Mountpoint) error {
	mnt, err := newFS("tmpfs", [][2]string{{"mode", "0755"}, {"size", "64k"}}, attrNoSuid|attrNoDev|attrNoExec)
	if err != nil {
		return err
	}
	defer unix.Close(mnt)
	mkdir := func(rel string) error {
		err := unix.Mkdirat(mnt, rel, 0o755)
		if err == unix.EEXIST {
			return nil
		}
		if err == nil {
			// The daemon's umask must not narrow the path the view's identity
			// searches.
			err = unix.Fchmodat(mnt, rel, 0o755, 0)
		}
		if err != nil {
			return &Error{Kind: ErrLauncher, Op: "mkdir", Path: "/" + rel, Err: err}
		}
		return nil
	}
	for _, m := range mps {
		rel := strings.TrimPrefix(m.Path, "/")
		for i, c := range rel {
			if c == '/' {
				if err := mkdir(rel[:i]); err != nil {
					return err
				}
			}
		}
		if m.Dir {
			err = mkdir(rel)
		} else {
			err = createFile(mnt, rel, m.Path)
		}
		if err != nil {
			return err
		}
	}
	if err := readOnly(mnt, "/"); err != nil {
		return err
	}
	if err := unix.MoveMount(mnt, "", unix.AT_FDCWD, staging, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
		return mountError("move_mount", "/", err)
	}
	return nil
}

// at returns where the world presents the mountpoint at view path p.
func (b *builder) at(p string) (string, error) {
	if t, ok := b.targets[p]; ok {
		return t, nil
	}
	return "", &Error{Kind: ErrLauncher, Op: "present", Path: p, Err: errors.New("the world presents no target")}
}

// shimDir presents the shim at /.oac/bin/<name> on a read-only tmpfs.
func (b *builder) shimDir(names []string, shim int) error {
	dir, err := b.at(agent.ViewPrivateRoot + "/" + agent.ViewShimName)
	if err != nil {
		return err
	}
	mnt, err := newFS("tmpfs", [][2]string{{"mode", "0755"}, {"size", "64k"}}, attrNoSuid|attrNoDev|attrNoExec)
	if err != nil {
		return err
	}
	defer unix.Close(mnt)
	if err := b.attach(mnt, b.root, dir, true); err != nil {
		return err
	}
	for _, n := range names {
		if err := createFile(mnt, n, dir+"/"+n); err != nil {
			return err
		}
		if err := b.bindAt(shim, mnt, n, dir+"/"+n, bindAttr(false, true, false)); err != nil {
			return err
		}
	}
	return readOnly(mnt, dir)
}

// runDir presents the relay's listening socket at processshim.SocketPath on a read-only tmpfs, so that the process can connect to it but not replace it. Only the process's user may connect. The socket is created through the mount, never through a path the world resolves.
func (b *builder) runDir(uid, gid uint32) error {
	dir, err := b.at(agent.ViewPrivateRoot + "/" + agent.ViewRunName)
	if err != nil {
		return err
	}
	mnt, err := newFS("tmpfs", [][2]string{{"mode", "0755"}, {"size", "64k"}}, attrNoSuid|attrNoDev|attrNoExec)
	if err != nil {
		return err
	}
	defer unix.Close(mnt)
	if err := b.attach(mnt, b.root, dir, true); err != nil {
		return err
	}
	fail := func(op string, err error) error {
		return &Error{Kind: ErrLauncher, Op: op, Path: processshim.SocketPath, Err: err}
	}
	ln, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fail("socket", err)
	}
	b.listener = ln
	if err := unix.Bind(ln, &unix.SockaddrUnix{Name: fmt.Sprintf("/proc/self/fd/%d/%s", mnt, processshim.SocketName)}); err != nil {
		return fail("bind", err)
	}
	if err := unix.Fchownat(mnt, processshim.SocketName, int(uid), int(gid), unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fail("chown", err)
	}
	if err := unix.Fchmodat(mnt, processshim.SocketName, 0o600, 0); err != nil {
		return fail("chmod", err)
	}
	if err := unix.Listen(ln, unix.SOMAXCONN); err != nil {
		return fail("listen", err)
	}
	return readOnly(mnt, dir)
}

// dev builds a minimal read-only /dev with host device nodes, a new devpts instance and a noexec /dev/shm.
func (b *builder) dev() error {
	at, err := b.at(agent.ViewDevRoot)
	if err != nil {
		return err
	}
	mnt, err := newFS("tmpfs", [][2]string{{"mode", "0755"}, {"size", "64k"}}, attrNoSuid|attrNoDev|attrNoExec)
	if err != nil {
		return err
	}
	defer unix.Close(mnt)
	if err := b.attach(mnt, b.root, at, true); err != nil {
		return err
	}
	for _, n := range devNodes {
		src, err := b.source("/dev/" + n)
		if err != nil {
			return err
		}
		if err := createFile(mnt, n, "/dev/"+n); err != nil {
			return err
		}
		if err := b.bindAt(src, mnt, n, "/dev/"+n, bindAttr(true, false, true)); err != nil {
			return err
		}
	}
	subs := []struct {
		name, fstype string
		opts         [][2]string
		attr         int
	}{
		{"pts", "devpts", [][2]string{{"ptmxmode", "0666"}, {"mode", "0620"}}, attrNoSuid | attrNoExec},
		{"shm", "tmpfs", [][2]string{{"mode", "1777"}}, attrNoSuid | attrNoDev | attrNoExec},
	}
	for _, s := range subs {
		if err := unix.Mkdirat(mnt, s.name, 0o755); err != nil {
			return &Error{Kind: ErrLauncher, Op: "mkdir", Path: "/dev/" + s.name, Err: err}
		}
		fs, err := newFS(s.fstype, s.opts, s.attr)
		if err != nil {
			return err
		}
		err = b.attachAt(fs, mnt, s.name, "/dev/"+s.name, true)
		unix.Close(fs)
		if err != nil {
			return err
		}
	}
	for _, l := range devLinks {
		if err := unix.Symlinkat(l[1], mnt, l[0]); err != nil {
			return &Error{Kind: ErrLauncher, Op: "symlink", Path: "/dev/" + l[0], Err: err}
		}
	}
	return readOnly(mnt, agent.ViewDevRoot)
}

// source opens a trusted host path.
func (b *builder) source(path string) (int, error) {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, &Error{Kind: ErrLauncher, Op: "open", Path: path, Err: err}
	}
	return b.keep(fd), nil
}

func (b *builder) keep(fd int) int {
	b.fds = append(b.fds, fd)
	return fd
}

// closeBuild closes what only the build needs.
func (b *builder) closeBuild() {
	for _, fd := range b.fds {
		unix.Close(fd)
	}
	b.fds, b.root = nil, -1
}

func (b *builder) closeListener() {
	if b.listener >= 0 {
		unix.Close(b.listener)
		b.listener = -1
	}
}

func (b *builder) close() {
	b.closeBuild()
	b.closeListener()
	if b.proc >= 0 {
		unix.Close(b.proc)
		b.proc = -1
	}
}

// bind mounts a clone of src at the absolute view path under the world root.
func (b *builder) bind(src, root int, view string, attr *unix.MountAttr) error {
	return b.bindAt(src, root, strings.TrimPrefix(view, "/"), view, attr)
}

func (b *builder) bindAt(src, dirfd int, rel, view string, attr *unix.MountAttr) error {
	var st unix.Stat_t
	if err := unix.Fstat(src, &st); err != nil {
		return &Error{Kind: ErrLauncher, Op: "stat source", Path: view, Err: err}
	}
	mnt, err := unix.OpenTree(src, "", unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_EMPTY_PATH)
	if err != nil {
		return mountError("open_tree", view, err)
	}
	defer unix.Close(mnt)
	if err := unix.MountSetattr(mnt, "", unix.AT_EMPTY_PATH, attr); err != nil {
		return mountError("mount_setattr", view, err)
	}
	return b.attachAt(mnt, dirfd, rel, view, st.Mode&unix.S_IFMT == unix.S_IFDIR)
}

func (b *builder) attach(mnt, root int, view string, dir bool) error {
	return b.attachAt(mnt, root, strings.TrimPrefix(view, "/"), view, dir)
}

// attachAt moves a detached mount onto rel beneath dirfd, which must be a directory when dir is set and a regular file otherwise.
func (b *builder) attachAt(mnt, dirfd int, rel, view string, dir bool) error {
	target, err := resolve(dirfd, rel)
	if err != nil {
		return &Error{Kind: ErrMountTarget, Op: "resolve", Path: view, Err: err}
	}
	defer unix.Close(target)
	var st unix.Stat_t
	if err := unix.Fstat(target, &st); err != nil {
		return &Error{Kind: ErrMountTarget, Op: "stat", Path: view, Err: err}
	}
	switch kind := st.Mode & unix.S_IFMT; {
	case dir && kind != unix.S_IFDIR:
		return &Error{Kind: ErrMountTarget, Op: "check", Path: view, Err: unix.ENOTDIR}
	case !dir && kind != unix.S_IFREG:
		return &Error{Kind: ErrMountTarget, Op: "check", Path: view, Err: fmt.Errorf("not a regular file")}
	}
	if err := unix.MoveMount(mnt, "", target, "", unix.MOVE_MOUNT_F_EMPTY_PATH|unix.MOVE_MOUNT_T_EMPTY_PATH); err != nil {
		return mountError("move_mount", view, err)
	}
	return nil
}

// resolve opens rel beneath dirfd without following symlinks or crossing mounts.
func resolve(dirfd int, rel string) (int, error) {
	how := &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	}
	// openat2 reports a concurrent rename with EAGAIN; a sandbox that keeps renaming fails the view.
	for try := 0; ; try++ {
		fd, err := unix.Openat2(dirfd, rel, how)
		if (err == unix.EAGAIN || err == unix.EINTR) && try < 16 {
			continue
		}
		return fd, err
	}
}

func newFS(fstype string, opts [][2]string, attr int) (int, error) {
	fs, err := unix.Fsopen(fstype, unix.FSOPEN_CLOEXEC)
	if err != nil {
		return -1, mountError("fsopen", fstype, err)
	}
	defer unix.Close(fs)
	for _, o := range opts {
		if err := unix.FsconfigSetString(fs, o[0], o[1]); err != nil {
			return -1, mountError("fsconfig "+o[0], fstype, err)
		}
	}
	if err := unix.FsconfigCreate(fs); err != nil {
		return -1, mountError("fsconfig create", fstype, err)
	}
	mnt, err := unix.Fsmount(fs, unix.FSMOUNT_CLOEXEC, attr)
	if err != nil {
		return -1, mountError("fsmount", fstype, err)
	}
	return mnt, nil
}

func createFile(dirfd int, name, view string) error {
	fd, err := unix.Openat(dirfd, name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o444)
	if err != nil {
		return &Error{Kind: ErrLauncher, Op: "create", Path: view, Err: err}
	}
	return unix.Close(fd)
}

func readOnly(mnt int, view string) error {
	if err := unix.MountSetattr(mnt, "", unix.AT_EMPTY_PATH, &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY}); err != nil {
		return mountError("mount_setattr", view, err)
	}
	return nil
}

func bindAttr(writable, exec, dev bool) *unix.MountAttr {
	a := &unix.MountAttr{Attr_set: attrNoSuid}
	for _, f := range []struct {
		set  bool
		attr uint64
	}{{!writable, unix.MOUNT_ATTR_RDONLY}, {!exec, attrNoExec}, {!dev, attrNoDev}} {
		if f.set {
			a.Attr_set |= f.attr
		} else {
			a.Attr_clr |= f.attr
		}
	}
	return a
}

// switchRoot makes the world root the process root. pivot_root would also detach the old root, but container seccomp profiles block it.
func switchRoot(root int) error {
	if err := unix.Fchdir(root); err != nil {
		return &Error{Kind: ErrLauncher, Op: "fchdir", Path: "/", Err: err}
	}
	if err := unix.Mount(".", "/", "", unix.MS_MOVE, ""); err != nil {
		return mountError("move root", "/", err)
	}
	if err := unix.Chroot("."); err != nil {
		return &Error{Kind: ErrLauncher, Op: "chroot", Path: "/", Err: err}
	}
	if err := unix.Chdir("/"); err != nil {
		return &Error{Kind: ErrLauncher, Op: "chdir", Path: "/", Err: err}
	}
	return nil
}

func mountError(op, path string, err error) error {
	kind := ErrLauncher
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
		kind = ErrMountDenied
	}
	return &Error{Kind: kind, Op: op, Path: path, Err: err}
}
