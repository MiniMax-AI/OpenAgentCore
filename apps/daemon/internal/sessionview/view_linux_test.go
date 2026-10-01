//go:build linux

package sessionview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	gofs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// The view tests need root with CAP_SYS_ADMIN and CAP_NET_ADMIN, /dev/fuse and no AppArmor confinement. Run them in a throwaway container:
//
//	CGO_ENABLED=0 go test -c -o /tmp/sessionview.test ./apps/daemon/internal/sessionview
//	docker run --rm --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
//	  -e OAC_TEST_SESSIONVIEW=1 -v /tmp/sessionview.test:/t.test:ro debian:bookworm-slim /t.test -test.v
const (
	gateEnv    = "OAC_TEST_SESSIONVIEW"
	helperEnv  = "OAC_VIEW_HELPER"
	shimMarker = "oac-test-shim"
	viewID     = 1000
	brokerAddr = "127.0.0.1:7070"
)

// The test binary is also the Harness, the shim and the world binary inside the view.
func TestMain(m *testing.M) {
	Init()
	switch filepath.Base(os.Args[0]) {
	case "sh", "env":
		fmt.Println(shimMarker)
		os.Exit(0)
	}
	if mode := os.Getenv(helperEnv); mode != "" {
		os.Exit(runHelper(mode))
	}
	os.Exit(m.Run())
}

func TestViewIsolation(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	w := &loopbackWorld{dir: f.world}
	spec := f.spec(w, "probe", "OAC_VIEW_HOST_PATH="+f.self)
	spec.Network.Setup = serveBroker(t)
	v, err := Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	out, err := io.ReadAll(v.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if exit, err := v.Wait(); err != nil || exit != (Exit{}) {
		t.Fatalf("Wait = %+v, %v; output %s", exit, err, out)
	}
	var report map[string]string
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("probe output %q: %v", out, err)
	}
	for _, c := range viewChecks {
		if msg, ok := report[c.name]; !ok || msg != "" {
			t.Errorf("%s: %q", c.name, msg)
		}
	}
	if got, err := os.ReadFile(filepath.Join(f.world, "data", "out.txt")); err != nil || string(got) != "written in the view" {
		t.Errorf("world file written in the view = %q, %v", got, err)
	}
}

func TestViewSignalAndTeardown(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	w := &loopbackWorld{dir: f.world}
	token := fmt.Sprintf("oac-grandchild-%d", time.Now().UnixNano())
	v, err := Start(context.Background(), f.spec(w, "wait", "OAC_VIEW_TOKEN="+token))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	if line, err := bufio.NewReader(v.Stdout()).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("harness said %q, %v", line, err)
	}
	if n := processesWith(t, token); n != 1 {
		t.Fatalf("%d grandchildren before exit, want 1", n)
	}
	if staged, _ := os.ReadDir(f.staging); len(staged) != 1 {
		t.Fatalf("staging parent holds %v, want the view's staging directory", staged)
	}
	if err := v.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if exit, err := v.Wait(); err != nil || exit != (Exit{Code: 7}) {
		t.Fatalf("Wait = %+v, %v; want exit code 7", exit, err)
	}
	if n := processesWith(t, token); n != 0 {
		t.Errorf("%d grandchildren survived the Harness", n)
	}
	select {
	case <-w.served:
	default:
		t.Error("world server still serving")
	}
	if left, _ := os.ReadDir(f.staging); len(left) != 0 {
		t.Errorf("staging directories left: %v", left)
	}
}

// TestViewDescendantsKeepTheGrace checks that a helper still cleaning up when the Harness exits gets TERM and finishes within the grace.
func TestViewDescendantsKeepTheGrace(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	w := &loopbackWorld{dir: f.world}
	spec := f.spec(w, "cleanup")
	spec.Process.Grace = 10 * time.Second
	v, err := Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	if line, err := bufio.NewReader(v.Stdout()).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("helper said %q, %v", line, err)
	}
	started := time.Now()
	if err := v.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if exit, err := v.Wait(); err != nil || exit != (Exit{Code: 7}) {
		t.Fatalf("Wait = %+v, %v; want exit code 7", exit, err)
	}
	if elapsed := time.Since(started); elapsed >= spec.Process.Grace {
		t.Errorf("view ended after %v, want once the helper exited", elapsed)
	}
	if got, err := os.ReadFile(filepath.Join(f.world, "data", "cleaned")); err != nil || string(got) != "done" {
		t.Errorf("helper cleanup = %q, %v; want it finished", got, err)
	}
}

// TestViewRefusesSymlinkedMountpoint checks that a sandbox symlink on the way to a mountpoint fails the view instead of redirecting the mount.
func TestViewRefusesSymlinkedMountpoint(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	etc := filepath.Join(f.world, "etc")
	if err := os.Rename(etc, etc+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("etc.real", etc); err != nil {
		t.Fatal(err)
	}
	w := &loopbackWorld{dir: f.world}
	if _, err := Start(context.Background(), f.spec(w, "noop")); !errors.Is(err, ErrMountTarget) || !errors.Is(err, unix.ELOOP) {
		t.Fatalf("Start = %v, want ErrMountTarget with ELOOP", err)
	}
	select {
	case <-w.served:
	default:
		t.Error("world server still serving")
	}
}

// TestSeccompProgram runs the filter in a BPF interpreter for both architectures. The interpreter loads words big-endian where the kernel loads them in host order, so the input stores each word the filter reads big-endian at its seccomp_data offset.
func TestSeccompProgram(t *testing.T) {
	vm, err := bpf.NewVM(seccompProgram())
	if err != nil {
		t.Fatal(err)
	}
	amd64, arm64 := seccompArches[0], seccompArches[1]
	cases := []struct {
		name           string
		arch, nr, arg0 uint32
		want           uint32
	}{
		{"amd64 getpid", amd64.audit, 39, 0, retAllow},
		{"amd64 clone", amd64.audit, amd64.clone, unix.CLONE_VM | unix.CLONE_VFORK, retAllow},
		{"amd64 clone user namespace", amd64.audit, amd64.clone, unix.CLONE_NEWUSER, retEPERM},
		{"amd64 unshare mount namespace", amd64.audit, amd64.unshare, unix.CLONE_NEWNS, retAllow},
		{"amd64 unshare user namespace", amd64.audit, amd64.unshare, unix.CLONE_NEWNS | unix.CLONE_NEWUSER, retEPERM},
		{"amd64 setns", amd64.audit, amd64.setns, 0, retEPERM},
		{"amd64 clone3", amd64.audit, amd64.clone3, 0, retENOSYS},
		{"amd64 x32 unshare", amd64.audit, x32SyscallBit | amd64.unshare, unix.CLONE_NEWUSER, retENOSYS},
		{"arm64 getpid", arm64.audit, 172, 0, retAllow},
		{"arm64 clone", arm64.audit, arm64.clone, unix.CLONE_VM | unix.CLONE_VFORK, retAllow},
		{"arm64 clone user namespace", arm64.audit, arm64.clone, unix.CLONE_NEWUSER, retEPERM},
		{"arm64 unshare user namespace", arm64.audit, arm64.unshare, unix.CLONE_NEWUSER, retEPERM},
		{"arm64 setns", arm64.audit, arm64.setns, 0, retEPERM},
		{"arm64 clone3", arm64.audit, arm64.clone3, 0, retENOSYS},
		{"i386", unix.AUDIT_ARCH_I386, 1, 0, retKill},
		{"arm", unix.AUDIT_ARCH_ARM, 1, 0, retKill},
	}
	for _, c := range cases {
		in := make([]byte, 64)
		binary.BigEndian.PutUint32(in[dataNr:], c.nr)
		binary.BigEndian.PutUint32(in[dataArch:], c.arch)
		binary.BigEndian.PutUint32(in[dataArg0Low:], c.arg0)
		if got, err := vm.Run(in); err != nil || uint32(got) != c.want {
			t.Errorf("%s: %#x, %v; want %#x", c.name, got, err, c.want)
		}
	}
}

func TestStartRejectsInvalidSpec(t *testing.T) {
	for name, spec := range map[string]Spec{
		"relative overlay":            {Overlays: []Overlay{{Path: "etc/resolv.conf", Source: "/etc/hosts"}}},
		"writable executable private": {Private: []PrivateDir{{Name: "home", HostDir: t.TempDir(), Writable: true, Exec: true}}},
		"missing staging parent":      {StagingParent: filepath.Join(t.TempDir(), "missing")},
	} {
		if spec.StagingParent == "" {
			spec.StagingParent = t.TempDir()
		}
		spec.World = (&loopbackWorld{}).serve
		spec.Process = Process{Path: "/bin/true", Args: []string{"true"}, Dir: "/", UID: viewID, GID: viewID}
		if _, err := Start(context.Background(), spec); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("%s: Start = %v, want ErrInvalidSpec", name, err)
		}
	}
}

func requireView(t *testing.T) {
	t.Helper()
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a privileged container; see the comment at the top of this file", gateEnv)
	}
	if err := Probe(); err != nil {
		t.Fatalf("Probe: %v", err)
	}
}

type fixture struct {
	self, world, harness, home, run, overlay, staging string
}

// newFixture lays out a world with the mountpoints the real world frontend presents synthetically, plus the local sources.
func newFixture(t *testing.T) *fixture {
	base := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		self:    self,
		world:   filepath.Join(base, "world"),
		harness: filepath.Join(base, "harness"),
		home:    filepath.Join(base, "home"),
		run:     filepath.Join(base, "run"),
		overlay: filepath.Join(base, "overlay"),
		staging: filepath.Join(base, "staging"),
	}
	for _, d := range []string{".oac/harness", ".oac/home", ".oac/run", ".oac/bin", "proc", "dev", "bin", "usr/bin", "data", "etc/oac-overlay"} {
		mkdir(t, filepath.Join(f.world, d))
	}
	writeFile(t, filepath.Join(f.world, "bin", "sh"), "")
	writeFile(t, filepath.Join(f.world, "usr", "bin", "env"), "")
	writeFile(t, filepath.Join(f.world, "data", "in.txt"), "hello from the world")
	copyFile(t, self, filepath.Join(f.world, "bin", "worldbin"))
	mkdir(t, f.harness)
	copyFile(t, self, filepath.Join(f.harness, "harness"))
	for _, d := range []string{f.home, f.run} {
		mkdir(t, d)
		if err := os.Chown(d, viewID, viewID); err != nil {
			t.Fatal(err)
		}
	}
	mkdir(t, f.overlay)
	mkdir(t, f.staging)
	writeFile(t, filepath.Join(f.overlay, "greeting"), "from the overlay")
	return f
}

func (f *fixture) spec(w *loopbackWorld, mode string, env ...string) Spec {
	return Spec{
		World: w.serve,
		Private: []PrivateDir{
			{Name: "harness", HostDir: f.harness, Exec: true},
			{Name: "home", HostDir: f.home, Writable: true},
			{Name: "run", HostDir: f.run, Writable: true},
		},
		Overlays: []Overlay{{Path: "/etc/oac-overlay", Source: f.overlay}},
		Shim: Shim{
			Binary: filepath.Join(f.harness, "harness"),
			Names:  []string{"sh", "env"},
			Paths:  []string{"/bin/sh", "/usr/bin/env"},
		},
		Process: Process{
			Path:   "/.oac/harness/harness",
			Args:   []string{"harness"},
			Env:    append([]string{helperEnv + "=" + mode}, env...),
			Dir:    "/data",
			UID:    viewID,
			GID:    viewID,
			Stderr: os.Stderr,
		},
		StagingParent: f.staging,
	}
}

// loopbackWorld serves a directory as the world, the way the world frontend serves a sandbox.
type loopbackWorld struct {
	dir    string
	served chan struct{}
}

func (w *loopbackWorld) serve(dev *os.File, _ WorldMount) (WorldServer, error) {
	fd, err := unix.Dup(int(dev.Fd()))
	if err != nil {
		return nil, err
	}
	root, err := gofs.NewLoopbackRoot(w.dir)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	srv, err := fuse.NewServer(gofs.NewNodeFS(root, &gofs.Options{}), fmt.Sprintf("/dev/fd/%d", fd), &fuse.MountOptions{})
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	w.served = make(chan struct{})
	go func() {
		srv.Serve()
		close(w.served)
	}()
	return w, nil
}

func (w *loopbackWorld) Stop() error {
	select {
	case <-w.served:
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("world still serving 10s after the view ended")
	}
}

// serveBroker listens in the view's network namespace, as the broker does.
func serveBroker(t *testing.T) func(*os.File) error {
	return func(netns *os.File) error {
		type result struct {
			ln  net.Listener
			err error
		}
		ch := make(chan result, 1)
		go func() {
			// Never unlocked: the thread exits with the goroutine instead of returning to the pool inside the view's namespace.
			runtime.LockOSThread()
			if err := unix.Setns(int(netns.Fd()), unix.CLONE_NEWNET); err != nil {
				ch <- result{err: err}
				return
			}
			ln, err := net.Listen("tcp", brokerAddr)
			ch <- result{ln, err}
		}()
		r := <-ch
		if r.err != nil {
			return r.err
		}
		t.Cleanup(func() { r.ln.Close() })
		go func() {
			for {
				c, err := r.ln.Accept()
				if err != nil {
					return
				}
				c.Write([]byte("oac-broker\n"))
				c.Close()
			}
		}()
		return nil
	}
}

// processesWith counts processes whose command line contains token.
func processesWith(t *testing.T, token string) int {
	t.Helper()
	cmdlines, err := filepath.Glob("/proc/[0-9]*/cmdline")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range cmdlines {
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte(token)) {
			n++
		}
	}
	return n
}

func runHelper(mode string) int {
	switch mode {
	case "probe":
		report := map[string]string{}
		for _, c := range viewChecks {
			report[c.name] = ""
			if err := c.run(); err != nil {
				report[c.name] = err.Error()
			}
		}
		json.NewEncoder(os.Stdout).Encode(report)
		return 0
	case "wait":
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM)
		child := exec.Command("/.oac/harness/harness", os.Getenv("OAC_VIEW_TOKEN"))
		child.Env = []string{helperEnv + "=sleep"}
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("ready")
		<-sigs
		return 7
	case "sleep":
		time.Sleep(time.Hour)
		return 0
	case "cleanup":
		// The Harness exits on TERM at once while its helper still cleans up.
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM)
		child := exec.Command("/.oac/harness/harness")
		child.Env = []string{helperEnv + "=slow-term"}
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		<-sigs
		return 7
	case "slow-term":
		sigs := make(chan os.Signal, 2)
		signal.Notify(sigs, syscall.SIGTERM)
		fmt.Println("ready")
		<-sigs
		time.Sleep(300 * time.Millisecond)
		if err := os.WriteFile("/data/cleaned", []byte("done"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	case "noop":
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown helper %q\n", mode)
	return 2
}

// viewChecks run inside the view as the Harness.
var viewChecks = []struct {
	name string
	run  func() error
}{
	{"inherits only stdio", onlyStdio},
	{"runs as the view user", func() error {
		if os.Getuid() != viewID || os.Getgid() != viewID {
			return fmt.Errorf("uid %d gid %d", os.Getuid(), os.Getgid())
		}
		return nil
	}},
	{"no capabilities and no_new_privs", noPrivileges},
	{"reads the world", func() error { return fileHas("/data/in.txt", "hello from the world") }},
	{"writes the world", func() error { return os.WriteFile("/data/out.txt", []byte("written in the view"), 0o644) }},
	{"root is the FUSE world", func() error {
		var st unix.Statfs_t
		if err := unix.Statfs("/", &st); err != nil {
			return err
		}
		if st.Type != unix.FUSE_SUPER_MAGIC {
			return fmt.Errorf("root file system type %#x", st.Type)
		}
		return nil
	}},
	{"world binaries do not execute", func() error { return execDenied("/bin/worldbin") }},
	{"/bin/sh runs the shim", func() error { return shimRuns("/bin/sh", "-c", "true") }},
	{"/.oac/bin runs the shim", func() error { return shimRuns("/.oac/bin/env") }},
	{"overlay is presented", func() error { return fileHas("/etc/oac-overlay/greeting", "from the overlay") }},
	{"/.oac/home is writable and not executable", func() error {
		if err := os.WriteFile("/.oac/home/tool", []byte("#!/.oac/bin/sh\n"), 0o755); err != nil {
			return err
		}
		return execDenied("/.oac/home/tool")
	}},
	{"moves and links across world directories", func() error { return moveAndLink("/data") }},
	{"moves and links across /.oac/home directories", func() error { return moveAndLink("/.oac/home") }},
	{"creates processes and threads", func() error {
		child := exec.Command("/.oac/harness/harness")
		child.Env = []string{helperEnv + "=noop"}
		if err := child.Run(); err != nil {
			return fmt.Errorf("os/exec child: %w", err)
		}
		// Locked goroutines each need a thread of their own.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tids, release := make(chan int), make(chan struct{})
		defer close(release)
		for range 8 {
			go func() {
				runtime.LockOSThread()
				tids <- unix.Gettid()
				<-release
			}()
		}
		seen := map[int]bool{unix.Gettid(): true}
		for range 8 {
			seen[<-tids] = true
		}
		if len(seen) != 9 {
			return fmt.Errorf("%d distinct threads, want 9", len(seen))
		}
		return nil
	}},
	{"denies user namespaces, setns and clone3", func() error {
		var errs []error
		// Without the filter, unshare(CLONE_NEWUSER) fails with EINVAL in a multithreaded process, a zero-sized clone3 with EINVAL and setns on a pipe with EINVAL.
		if err := unix.Unshare(unix.CLONE_NEWUSER); err != unix.EPERM {
			errs = append(errs, fmt.Errorf("unshare(CLONE_NEWUSER): %v", err))
		}
		child := exec.Command("/.oac/harness/harness")
		child.Env = []string{helperEnv + "=noop"}
		child.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWUSER}
		if err := child.Run(); !errors.Is(err, syscall.EPERM) {
			errs = append(errs, fmt.Errorf("clone(CLONE_NEWUSER): %v", err))
		}
		if _, _, err := unix.RawSyscall(unix.SYS_CLONE3, 0, 0, 0); err != unix.ENOSYS {
			errs = append(errs, fmt.Errorf("clone3: %v", err))
		}
		if err := unix.Setns(0, 0); err != unix.EPERM {
			errs = append(errs, fmt.Errorf("setns: %v", err))
		}
		return errors.Join(errs...)
	}},
	{"/proc shows only the view", func() error {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return err
		}
		var pids []int
		for _, e := range entries {
			if pid, err := strconv.Atoi(e.Name()); err == nil {
				pids = append(pids, pid)
			}
		}
		if !slices.Equal(pids, []int{1, os.Getpid()}) {
			return fmt.Errorf("pids %v", pids)
		}
		return nil
	}},
	{"host files are hidden", func() error {
		if _, err := os.Stat(os.Getenv("OAC_VIEW_HOST_PATH")); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat host path: %v", err)
		}
		return nil
	}},
	{"only loopback", func() error {
		ifs, err := net.Interfaces()
		if err != nil {
			return err
		}
		if len(ifs) != 1 || ifs[0].Name != "lo" || ifs[0].Flags&net.FlagUp == 0 {
			return fmt.Errorf("interfaces %v", ifs)
		}
		return nil
	}},
	{"reaches the broker on loopback", func() error {
		c, err := net.DialTimeout("tcp", brokerAddr, 5*time.Second)
		if err != nil {
			return err
		}
		defer c.Close()
		line, err := bufio.NewReader(c).ReadString('\n')
		if line != "oac-broker\n" {
			return fmt.Errorf("broker said %q, %v", line, err)
		}
		return nil
	}},
	{"no route out", func() error {
		if _, err := net.DialTimeout("tcp", "192.0.2.1:80", 2*time.Second); !errors.Is(err, syscall.ENETUNREACH) {
			return fmt.Errorf("dial: %v", err)
		}
		return nil
	}},
}

// onlyStdio finds inherited fds. Package initialisers in this binary open fds of their own, but Go opens every fd close-on-exec, so an fd without FD_CLOEXEC crossed the exec.
func onlyStdio() error {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return err
	}
	var inherited []string
	for _, e := range entries {
		fd, _ := strconv.Atoi(e.Name())
		if fd <= 2 {
			continue
		}
		if flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil && flags&unix.FD_CLOEXEC == 0 {
			link, _ := os.Readlink("/proc/self/fd/" + e.Name())
			inherited = append(inherited, e.Name()+"="+link)
		}
	}
	if len(inherited) > 0 {
		return fmt.Errorf("inherited fds %v", inherited)
	}
	return nil
}

func noPrivileges() error {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return err
	}
	want := map[string]string{"CapInh": "0000000000000000", "CapPrm": "0000000000000000", "CapEff": "0000000000000000", "CapBnd": "0000000000000000", "CapAmb": "0000000000000000", "NoNewPrivs": "1"}
	for _, line := range strings.Split(string(status), "\n") {
		k, v, _ := strings.Cut(line, ":")
		if w, ok := want[k]; ok {
			if strings.TrimSpace(v) != w {
				return fmt.Errorf("%s: %s", k, strings.TrimSpace(v))
			}
			delete(want, k)
		}
	}
	if len(want) > 0 {
		return fmt.Errorf("status lacks %v", want)
	}
	return nil
}

// moveAndLink renames a file into a sibling directory and hard-links it back.
func moveAndLink(dir string) error {
	a, b := filepath.Join(dir, "mv-a"), filepath.Join(dir, "mv-b")
	for _, d := range []string{a, b} {
		if err := os.Mkdir(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(a, "f"), []byte("moved"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(a, "f"), filepath.Join(b, "f")); err != nil {
		return err
	}
	if err := os.Link(filepath.Join(b, "f"), filepath.Join(a, "link")); err != nil {
		return err
	}
	return fileHas(filepath.Join(a, "link"), "moved")
}

func execDenied(path string) error {
	if err := exec.Command(path).Run(); !errors.Is(err, syscall.EACCES) {
		return fmt.Errorf("exec %s: %v, want EACCES", path, err)
	}
	return nil
}

func shimRuns(path string, args ...string) error {
	out, err := exec.Command(path, args...).Output()
	if err != nil || strings.TrimSpace(string(out)) != shimMarker {
		return fmt.Errorf("%s printed %q, %v", path, out, err)
	}
	return nil
}

func fileHas(path, want string) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(got) != want {
		return fmt.Errorf("%s holds %q", path, got)
	}
	return nil
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
}
