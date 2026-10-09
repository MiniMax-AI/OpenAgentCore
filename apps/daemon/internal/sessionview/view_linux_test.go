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
	"maps"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	gofs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview/sessionviewtest"
)

// The view tests need root with CAP_SYS_ADMIN and CAP_NET_ADMIN, /dev/fuse, no AppArmor confinement and a private cgroup namespace, in which sessionviewtest mounts a writable cgroup v2 hierarchy for the views. Run them in a throwaway container:
//
//	CGO_ENABLED=0 go test -c -o /tmp/sessionview.test ./apps/daemon/internal/sessionview
//	docker run --rm --cgroupns=private --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
//	  -e OAC_TEST_SESSIONVIEW=1 -v /tmp/sessionview.test:/t.test:ro debian:bookworm-slim /t.test -test.v
const (
	gateEnv    = "OAC_TEST_SESSIONVIEW"
	helperEnv  = "OAC_VIEW_HELPER"
	shimMarker = "oac-test-shim"
	viewID     = 1000
	brokerAddr = "127.0.0.1:7070"
)

// The test binary is also the Harness, the shim, the relay and the world binary inside the view.
func TestMain(m *testing.M) {
	Init()
	if processshim.Relaying() {
		os.Exit(processshim.Relay())
	}
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
	sibling := filepath.Join(f.cgroups, "other-view")
	if err := unix.Mkdir(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	defer unix.Rmdir(sibling)
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
	if n := len(pidsWith(t, token)); n != 1 {
		t.Fatalf("%d grandchildren before exit, want 1", n)
	}
	if staged, _ := os.ReadDir(f.staging); len(staged) != 1 {
		t.Fatalf("staging parent holds %v, want the view's staging directory", staged)
	}
	if cgroups := sessionviewtest.Cgroups(t, f.cgroups); len(cgroups) != 1 {
		t.Fatalf("cgroup parent holds %v, want the view's cgroup", cgroups)
	} else if populated, err := isPopulated(cgroups[0]); err != nil || !populated {
		t.Fatalf("view cgroup populated = %v, %v; want the view's processes in it", populated, err)
	}
	if err := v.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if exit, err := v.Wait(); err != nil || exit != (Exit{Code: 7}) {
		t.Fatalf("Wait = %+v, %v; want exit code 7", exit, err)
	}
	if n := len(pidsWith(t, token)); n != 0 {
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
	if left := sessionviewtest.Cgroups(t, f.cgroups); len(left) != 0 {
		t.Errorf("view cgroups left: %v", left)
	}
}

// TestRelayLossIsReported checks that a relay lost while the process runs is reported, even when the process exits right after, and that the view's own end reports no loss.
func TestRelayLossIsReported(t *testing.T) {
	for _, lose := range []bool{false, true} {
		v, _ := startStalled(t)
		if lose {
			if err := syscall.Kill(relayPID(), syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			eventually(t, "the relay's end", func() bool { return relayPID() == 0 })
		}
		if err := v.Signal(syscall.SIGTERM); err != nil {
			t.Fatalf("Signal: %v", err)
		}
		v.Wait()
		reported := false
		select {
		case <-v.RelayLost():
			reported = true
		default:
		}
		if reported != lose {
			t.Errorf("relay killed: %v; RelayLost closed: %v", lose, reported)
		}
	}
}

// TestRelayStaysOpenUntilClose checks that the broker's end of the relay stays open once the view has ended, with its connection shut down, and that Close closes it.
func TestRelayStaysOpenUntilClose(t *testing.T) {
	v, _ := startStalled(t)
	if err := v.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	v.Wait()
	c, err := net.FileConn(v.Relay())
	if err != nil {
		t.Fatalf("the relay once the view has ended: %v", err)
	}
	defer c.Close()
	if n, err := c.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Errorf("reading the relay once the view has ended = %d, %v; want EOF", n, err)
	}
	v.Close()
	if _, err := v.Relay().Stat(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("the relay after Close: %v, want it closed", err)
	}
}

// TestViewDescendantsKeepTheGrace checks that helpers still cleaning up when their parent exits, the Harness or a spawned process, get TERM and finish within the grace.
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
	spawned, err := v.Spawn(context.Background(), "/.oac/harness/harness", []string{"harness"}, []string{helperEnv + "=cleanup", "OAC_VIEW_TOKEN=-spawned"}, "/data", false)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer closeStdio(spawned)
	for _, stdout := range []io.Reader{v.Stdout(), spawned.Stdout} {
		if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
			t.Fatalf("helper said %q, %v", line, err)
		}
	}
	started := time.Now()
	if err := v.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	// The Harness's exit shows while its helper still cleans up.
	for err := v.Signal(0); !errors.Is(err, os.ErrProcessDone); err = v.Signal(0) {
		if err != nil || time.Since(started) > 5*time.Second {
			t.Fatalf("Signal after the Harness exited = %v, want os.ErrProcessDone", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(f.world, "data", "cleaned")); err == nil {
		t.Error("Signal reported the exit only after the helper finished")
	}
	if exit, err := v.Wait(); err != nil || exit != (Exit{Code: 7}) {
		t.Fatalf("Wait = %+v, %v; want exit code 7", exit, err)
	}
	if elapsed := time.Since(started); elapsed >= spec.Process.Grace {
		t.Errorf("view ended after %v, want once the helper exited", elapsed)
	}
	for _, name := range []string{"cleaned", "cleaned-spawned"} {
		if got, err := os.ReadFile(filepath.Join(f.world, "data", name)); err != nil || string(got) != "done" {
			t.Errorf("helper cleanup %s = %q, %v; want it finished", name, got, err)
		}
	}
	if code, err := spawned.Wait(); err != nil || code != 7 {
		t.Errorf("spawned Wait = %d, %v; want exit code 7", code, err)
	}
	// Once the spawned process has exited, its handle no longer reaches the group it led.
	if err := spawned.Signal(syscall.SIGTERM); !errors.Is(err, ErrExited) {
		t.Errorf("Signal after the spawned process exited = %v, want ErrExited", err)
	}
}

// TestSpawnRunsInTheView checks that a spawned process runs as the process does, with its stdio and exit coming back, that its handle reaches nothing once it has exited, that a spawn whose pipes, command or directory fail fails alone, that one whose context ended before it began returns that error and leaves no process, and that the view's end ends it.
func TestSpawnRunsInTheView(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	w := &loopbackWorld{dir: f.world}
	v, err := Start(context.Background(), f.spec(w, "identity"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	token := fmt.Sprintf("oac-spawned-%d", time.Now().UnixNano())
	sleeper, err := spawnHelper(context.Background(), v, "identity", "/data", token)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer closeStdio(sleeper)
	var ids [2]map[string]string
	for i, stdout := range []io.Reader{v.Stdout(), sleeper.Stdout} {
		if err := json.NewDecoder(stdout).Decode(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	if len(ids[0]) != 20 || !maps.Equal(ids[0], ids[1]) || ids[0]["Groups"] != "" {
		t.Errorf("spawned process runs as and in %v, the process as and in %v; want both alike, with no supplementary groups", ids[1], ids[0])
	}
	report, err := v.Spawn(context.Background(), "/.oac/harness/harness", []string{"harness"}, []string{helperEnv + "=report"}, "/data", true)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	report.Stdin.Write([]byte("ping"))
	report.Stdin.Close()
	out, _ := io.ReadAll(report.Stdout)
	errOut, _ := io.ReadAll(report.Stderr)
	closeStdio(report)
	if string(out) != "ping /data hello from the world <nil>" || string(errOut) != "to stderr" {
		t.Errorf("spawned process wrote %q and %q", out, errOut)
	}
	if code, err := report.Wait(); err != nil || code != 3 {
		t.Fatalf("spawned Wait = %d, %v; want exit code 3", code, err)
	}
	if err := report.Signal(syscall.SIGKILL); !errors.Is(err, ErrExited) {
		t.Errorf("Signal after the spawned process exited = %v, want ErrExited", err)
	}
	private, err := spawnHelper(context.Background(), v, "cwd", "/.oac/home")
	if err != nil {
		t.Fatalf("Spawn in private home: %v", err)
	}
	privateOut, err := io.ReadAll(private.Stdout)
	closeStdio(private)
	if err != nil || string(privateOut) != "/.oac/home" {
		t.Fatalf("private Spawn cwd = %q, %v", privateOut, err)
	}
	if code, err := private.Wait(); err != nil || code != 0 {
		t.Fatalf("private Spawn Wait = %d, %v", code, err)
	}
	// The directory is entered as the process's user.
	if err := os.Mkdir(filepath.Join(f.harness, "root-only"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := spawnHelper(context.Background(), v, "noop", "/.oac/harness/root-only"); !errors.Is(err, ErrExec) || !errors.Is(err, syscall.EACCES) {
		t.Errorf("Spawn in a directory only root may enter = %v, want ErrExec with EACCES", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	late := fmt.Sprintf("oac-cancelled-%d", time.Now().UnixNano())
	for range 20 {
		if _, err := spawnHelper(cancelled, v, "sleep", "/data", late); !errors.Is(err, context.Canceled) {
			t.Fatalf("Spawn with a cancelled context = %v, want context.Canceled", err)
		}
	}
	// A directory longer than a control packet fails alone. This spawn begins once what the cancelled ones started is reaped.
	bounded, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := spawnHelper(bounded, v, "noop", "/"+strings.Repeat("x", 1<<20)); !errors.Is(err, ErrExec) || !errors.Is(err, syscall.ENAMETOOLONG) {
		t.Errorf("Spawn in a 1 MiB directory = %.200v, want ErrExec with ENAMETOOLONG", err)
	}
	if n := len(pidsWith(t, late)); n != 0 {
		t.Errorf("the cancelled spawns left %d processes", n)
	}
	if err := sleeper.Signal(0); err != nil {
		t.Errorf("Signal to the running spawned process = %v", err)
	}
	// A spawn that cannot make its pipes fails with the reason.
	var limit unix.Rlimit
	if err := unix.Prlimit(0, unix.RLIMIT_NOFILE, nil, &limit); err != nil {
		t.Fatal(err)
	}
	if err := unix.Prlimit(0, unix.RLIMIT_NOFILE, &unix.Rlimit{Max: limit.Max}, nil); err != nil {
		t.Fatal(err)
	}
	_, err = spawnHelper(context.Background(), v, "noop", "/data")
	if err := unix.Prlimit(0, unix.RLIMIT_NOFILE, &limit, nil); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(err, ErrLauncher) || !errors.Is(err, syscall.EMFILE) {
		t.Errorf("Spawn without descriptors = %v, want ErrLauncher with EMFILE", err)
	}
	// One argument above the control socket's packet size starts; one above exec's limit fails alone.
	for size, want := range map[int]error{100 << 10: nil, 200 << 10: ErrExec} {
		s, err := spawnHelper(context.Background(), v, "noop", "/data", strings.Repeat("x", size))
		if err == nil {
			closeStdio(s)
			_, err = s.Wait()
		}
		if !errors.Is(err, want) {
			t.Errorf("Spawn with a %d byte argument = %v, want %v", size, err, want)
		}
	}
	if err := v.Signal(0); err != nil || len(pidsWith(t, token)) != 1 {
		t.Fatalf("after the spawns, the view's Signal = %v and the sleeper runs %d times; want both running", err, len(pidsWith(t, token)))
	}
	if err := v.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := sleeper.Wait(); !errors.Is(err, ErrClosed) {
		t.Errorf("spawned Wait after the view ended = %v, want ErrClosed", err)
	}
	if n := len(pidsWith(t, token)); n != 0 {
		t.Errorf("%d spawned processes survived the view", n)
	}
}

// TestStalledSpawnBlocksNothingElse checks that while a spawn's directory is stuck on the world, the spawns behind it wait holding no descriptors and return once their contexts end, and that the view's signals, the exits of its other processes, the stuck caller's context, the process's exit and the teardown all go on.
func TestStalledSpawnBlocksNothingElse(t *testing.T) {
	v, w := startStalled(t)
	sleeper, err := spawnHelper(context.Background(), v, "sleep", "/data")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer closeStdio(sleeper)
	launcherFDs := fdCount(t, v.cmd.Process.Pid)
	ctx, cancel := context.WithCancel(context.Background())
	stuck := spawnAsync(ctx, v, "sleep", "/data/stall")
	await(t, w.stalled, "the spawn's lookup in the world")
	fds := fdCount(t, os.Getpid())
	waitCtx, stopWaiting := context.WithCancel(context.Background())
	var waiting []<-chan spawnResult
	for range 50 {
		waiting = append(waiting, spawnAsync(waitCtx, v, "noop", "/data"))
	}
	eventually(t, "50 spawns waiting", func() bool { return inSpawn() == 51 })
	if n := fdCount(t, os.Getpid()); n > fds {
		t.Errorf("%d descriptors with 50 spawns waiting, %d before", n, fds)
	}
	// The launcher holds the stuck spawn's descriptors, nothing for the spawns waiting.
	if n := fdCount(t, v.cmd.Process.Pid); n > launcherFDs+4 {
		t.Errorf("launcher holds %d descriptors with a spawn stuck and 50 waiting, %d before", n, launcherFDs)
	}
	stopWaiting()
	for _, r := range waiting {
		if r := await(t, r, "a waiting spawn's return"); !errors.Is(r.err, context.Canceled) {
			t.Errorf("waiting Spawn = %v, want context.Canceled", r.err)
		}
	}
	// Enough requests that the launcher's heap would call for a collection.
	signaled := make(chan error, 1)
	go func() {
		for range 1000 {
			if err := v.Signal(0); err != nil {
				signaled <- err
				return
			}
		}
		signaled <- nil
	}()
	if err := await(t, signaled, "1000 signals"); err != nil {
		t.Errorf("Signal while a spawn is stuck = %v", err)
	}
	if err := sleeper.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("Signal to the sleeper = %v", err)
	}
	if code := await(t, waitFor(sleeper), "the sleeper's exit"); code != -1 {
		t.Errorf("sleeper Wait = %d, want -1", code)
	}
	cancel()
	if r := await(t, stuck, "the stuck spawn's return"); !errors.Is(r.err, context.Canceled) {
		t.Errorf("stuck Spawn = %v, want context.Canceled", r.err)
	}
	if err := v.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	exited := make(chan error, 1)
	go func() {
		exit, err := v.Wait()
		if exit != (Exit{Code: 7}) {
			err = errors.Join(err, fmt.Errorf("exit %+v", exit))
		}
		exited <- err
	}()
	if err := await(t, exited, "the view's end"); err != nil {
		t.Errorf("Wait with a spawn still starting: %v, want exit code 7", err)
	}
}

// TestLateSpawnIsEnded checks that a process whose spawn was cancelled before it started is killed once it starts, before the next spawn begins.
func TestLateSpawnIsEnded(t *testing.T) {
	v, w := startStalled(t)
	token := fmt.Sprintf("oac-late-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	late := spawnAsync(ctx, v, "sleep", "/data/stall", token)
	await(t, w.stalled, "the spawn's lookup in the world")
	cancel()
	if r := await(t, late, "the cancelled spawn's return"); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("Spawn = %v, want context.Canceled", r.err)
	}
	next := spawnAsync(context.Background(), v, "noop", "/data")
	eventually(t, "the next spawn waiting", func() bool { return inSpawn() == 1 })
	w.unstall()
	r := await(t, next, "the next spawn")
	if r.err != nil {
		t.Fatalf("next Spawn: %v", r.err)
	}
	closeStdio(r.s)
	if n := len(pidsWith(t, token)); n != 0 {
		t.Errorf("the late process runs %d times once the next spawn started", n)
	}
	if code, err := r.s.Wait(); err != nil || code != 0 {
		t.Errorf("next Wait = %d, %v", code, err)
	}
}

// TestSpawnExitsBeforeItsRegistration checks that a spawned child that dies before its exec, while orphans exit around it, reports its own exit to its own handle and to no other, that a handle whose process has exited reaches nothing, and that Close ends what remains. While the child is stuck, its fork may hold up the launcher, so the test acts on the processes directly.
func TestSpawnExitsBeforeItsRegistration(t *testing.T) {
	v, w := startStalled(t)
	launcher := v.cmd.Process.Pid
	token := fmt.Sprintf("oac-bystander-%d", time.Now().UnixNano())
	bystander, err := spawnHelper(context.Background(), v, "sleep", "/data", token)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer closeStdio(bystander)
	orphanToken := fmt.Sprintf("oac-orphan-%d", time.Now().UnixNano())
	parent, err := v.Spawn(context.Background(), "/.oac/harness/harness", []string{"harness"}, []string{helperEnv + "=wait", "OAC_VIEW_TOKEN=" + orphanToken}, "/data", false)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer closeStdio(parent)
	if line, err := bufio.NewReader(parent.Stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("parent said %q, %v", line, err)
	}
	orphan := pidsWith(t, orphanToken)
	if len(orphan) != 1 {
		t.Fatalf("orphans: %v, want 1", orphan)
	}
	group, _ := strconv.Atoi(stat(orphan[0])[2])
	// The child's exec stays on the world.
	pending := make(chan spawnResult, 1)
	go func() {
		s, err := v.Spawn(context.Background(), "/data/stall", []string{"stall"}, nil, "/data", false)
		pending <- spawnResult{s, err}
	}()
	await(t, w.stalled, "the exec's lookup in the world")
	// The parent's group ends while the child forks; its orphan stays unreaped until the child is registered.
	if err := unix.Kill(-group, unix.SIGKILL); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the orphan's exit", func() bool { return slices.Contains(zombies(t, launcher), orphan[0]) })
	child := pidsWith(t, launcherArg0)
	child = slices.DeleteFunc(child, func(pid int) bool { return pid == launcher })
	if len(child) != 1 {
		t.Fatalf("children before their exec: %v, want 1", child)
	}
	// The child dies before its exec, once the world answers.
	if err := unix.Kill(child[0], unix.SIGKILL); err != nil {
		t.Fatal(err)
	}
	w.unstall()
	r := await(t, pending, "the spawn")
	if r.err != nil {
		t.Fatalf("Spawn = %v, want the child that died before its exec", r.err)
	}
	defer closeStdio(r.s)
	if code := await(t, waitFor(r.s), "the child's exit"); code != -1 {
		t.Errorf("child Wait = %d, want -1", code)
	}
	if code := await(t, waitFor(parent), "the parent's exit"); code != -1 {
		t.Errorf("parent Wait = %d, want -1", code)
	}
	for _, s := range []*Spawned{r.s, parent} {
		if err := s.Signal(0); !errors.Is(err, ErrExited) {
			t.Errorf("Signal after the process exited = %v, want ErrExited", err)
		}
	}
	if err := bystander.Signal(0); err != nil {
		t.Errorf("Signal to the bystander = %v", err)
	}
	eventually(t, "the orphans reaped", func() bool { return len(zombies(t, launcher)) == 0 })
	if err := v.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := bystander.Wait(); !errors.Is(err, ErrClosed) {
		t.Errorf("bystander Wait after Close = %v, want ErrClosed", err)
	}
	if n := len(pidsWith(t, token)); n != 0 {
		t.Errorf("%d bystanders survived the view", n)
	}
}

// TestTeardownIsBounded checks that a world server that never ends the request the view's process is blocked on fails the teardown with ErrCleanup within the bound instead of hanging it, and that the view's cgroup stays for Recover after its processes end past the bound.
func TestTeardownIsBounded(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	writeFile(t, filepath.Join(f.world, "data", "hang"), "")
	w := &hangWorld{loopbackWorld: loopbackWorld{dir: f.world}, hung: make(chan struct{}), release: make(chan struct{})}
	saved := closeWait
	closeWait = time.Second
	defer func() { closeWait = saved }()
	spec := f.spec(&w.loopbackWorld, "hang")
	spec.World = w.serve
	v, err := Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-w.hung:
	case <-time.After(10 * time.Second):
		t.Fatal("the process never wrote to the world")
	}
	started := time.Now()
	if err := v.Close(); !errors.Is(err, ErrCleanup) {
		t.Errorf("Close = %v, want ErrCleanup", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("Close took %v with a teardown bound of %v", elapsed, closeWait)
	}
	if _, err := v.Wait(); !errors.Is(err, ErrClosed) || !errors.Is(err, ErrCleanup) {
		t.Errorf("Wait = %v, want ErrClosed and ErrCleanup", err)
	}
	cgroups := sessionviewtest.Cgroups(t, f.cgroups)
	if len(cgroups) != 1 {
		t.Fatalf("cgroup parent holds %v after the bound, want the view's cgroup", cgroups)
	}
	// Once the world answers, the process ends, and its cgroup stays for Recover.
	close(w.release)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if populated, err := isPopulated(cgroups[0]); err != nil {
			t.Fatal(err)
		} else if !populated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the view's process outlived the world's answer")
		}
	}
	recoverKept(t, w, f.cgroups)
}

// TestStalledWorldStopKeepsTheCgroup checks that a world server that does not stop within the bound fails the teardown with ErrCleanup and keeps the view's cgroup, although the view's processes have ended.
func TestStalledWorldStopKeepsTheCgroup(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	w := &hangWorld{loopbackWorld: loopbackWorld{dir: f.world}, hung: make(chan struct{}), release: make(chan struct{})}
	saved := closeWait
	closeWait = time.Second
	defer func() { closeWait = saved }()
	spec := f.spec(&w.loopbackWorld, "noop")
	spec.World = w.serve
	v, err := Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	if exit, err := v.Wait(); exit != (Exit{}) || !errors.Is(err, ErrCleanup) {
		t.Errorf("Wait = %+v, %v; want exit code 0 and ErrCleanup", exit, err)
	}
	cgroups := sessionviewtest.Cgroups(t, f.cgroups)
	if len(cgroups) != 1 {
		t.Fatalf("cgroup parent holds %v after the bound, want the view's cgroup", cgroups)
	}
	if populated, err := isPopulated(cgroups[0]); err != nil || populated {
		t.Errorf("view cgroup populated = %v, %v; want its processes ended", populated, err)
	}
	close(w.release)
	recoverKept(t, w, f.cgroups)
}

// recoverKept waits until w has stopped serving after its release, checks that the view's cgroup is still there, and removes it with Recover.
func recoverKept(t *testing.T, w *hangWorld, parent string) {
	t.Helper()
	select {
	case <-w.served:
	case <-time.After(10 * time.Second):
		t.Fatal("the world server outlived its release")
	}
	if kept := sessionviewtest.Cgroups(t, parent); len(kept) != 1 {
		t.Fatalf("cgroup parent holds %v after the teardown, want the view's cgroup", kept)
	}
	if err := Recover(parent); err != nil {
		t.Errorf("Recover: %v", err)
	}
	if left := sessionviewtest.Cgroups(t, parent); len(left) != 0 {
		t.Errorf("view cgroups left after Recover: %v", left)
	}
}

// TestRecoverChecksTheParent checks that Recover refuses a plain directory and a cgroup that holds this process, and accepts a cgroup v2 directory the test owns, in which it leaves nothing.
func TestRecoverChecksTheParent(t *testing.T) {
	if err := Recover(t.TempDir()); !errors.Is(err, ErrCgroup) {
		t.Errorf("Recover of a plain directory = %v, want ErrCgroup", err)
	}
	requireView(t)
	parent := sessionviewtest.CgroupParent(t)
	// The test runs in the root of the cgroup v2 hierarchy that sessionviewtest mounts.
	if err := Recover(filepath.Dir(parent)); !errors.Is(err, ErrCgroup) {
		t.Errorf("Recover of this process's own cgroup = %v, want ErrCgroup", err)
	}
	if err := Recover(parent); err != nil {
		t.Errorf("Recover: %v", err)
	}
}

func TestClone3Error(t *testing.T) {
	if err := clone3Error(unix.EINVAL); err != nil {
		t.Errorf("clone3Error(EINVAL) = %v, want nil", err)
	}
	for _, errno := range []unix.Errno{unix.ENOSYS, unix.EPERM} {
		if err := clone3Error(errno); !errors.Is(err, ErrCgroup) || !errors.Is(err, errno) {
			t.Errorf("clone3Error(%v) = %v, want ErrCgroup", errno, err)
		}
	}
}

// TestRemoveCgroupKeepsItPastTheDeadline checks that an empty cgroup stays once the deadline has passed.
func TestRemoveCgroupKeepsItPastTheDeadline(t *testing.T) {
	requireView(t)
	dir := filepath.Join(sessionviewtest.CgroupParent(t), "view-late")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer unix.Rmdir(dir)
	if err := removeCgroup(dir, time.Now()); !errors.Is(err, ErrCleanup) {
		t.Errorf("removeCgroup = %v, want ErrCleanup", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("removeCgroup removed the cgroup past the deadline: %v", err)
	}
}

// TestCancelledStartStopsTheWorld checks that Start returns once its context ends while the launcher waits on a world that never answers during the build, and that it stops the world, which lets the launcher exit.
func TestCancelledStartStopsTheWorld(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	w := &stallWorld{loopbackWorld: loopbackWorld{dir: f.world}, name: "proc", stalled: make(chan struct{}), release: make(chan struct{})}
	spec := f.spec(&w.loopbackWorld, "noop")
	spec.World = w.serve
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan error, 1)
	go func() {
		v, err := Start(ctx, spec)
		if err == nil {
			v.Close()
		}
		started <- err
	}()
	select {
	case <-w.stalled:
	case <-time.After(10 * time.Second):
		t.Fatal("the launcher never looked up /proc in the world")
	}
	cancel()
	select {
	case err := <-started:
		if !errors.Is(err, ErrLauncher) || !errors.Is(err, context.Canceled) || errors.Is(err, ErrCleanup) {
			t.Errorf("Start = %v, want ErrLauncher with context.Canceled and no ErrCleanup", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Start did not return after its context ended")
	}
	select {
	case <-w.served:
	default:
		t.Error("world server still serving")
	}
}

// TestViewRefusesSymlinkedMountpoint checks that the launcher still refuses a symlink on the way to a target, so a world that reports a target it did not resolve cannot redirect a mount.
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

func TestWorldInitialDirectoryStaysInWorld(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	mkdir(t, filepath.Join(f.world, "data", "project"))
	for name, target := range map[string]string{"alias": "/.oac/home", "parent": "data"} {
		if err := os.Symlink(target, filepath.Join(f.world, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name, dir string
		err       error
	}{
		{"private alias", "/alias", unix.ELOOP},
		{"parent symlink", "/parent/project", unix.ELOOP},
		{"private mount", "/.oac/home", unix.EXDEV},
		{"overlay mount", "/etc/oac-overlay", unix.EXDEV},
		{"proc mount", "/proc", unix.EXDEV},
		{"sys mount", "/sys", unix.EXDEV},
		{"custom workspace", "/data/project", nil},
		{"world root", "/", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker := filepath.Join(f.home, "started")
			w := &loopbackWorld{dir: f.world}
			spec := f.spec(w, "cwd")
			spec.Process.Dir = test.dir
			v, err := Start(t.Context(), spec)
			if test.err != nil {
				if v != nil {
					v.Close()
				}
				if !errors.Is(err, ErrExec) || !errors.Is(err, test.err) {
					t.Fatalf("Start = %v, want ErrExec with %v", err, test.err)
				}
				if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("initial process ran: marker stat = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer v.Close()
			out, err := io.ReadAll(v.Stdout())
			if err != nil || string(out) != test.dir {
				t.Fatalf("cwd = %q, %v; want %q", out, err, test.dir)
			}
			if exit, err := v.Wait(); err != nil || exit != (Exit{}) {
				t.Fatalf("Wait = %+v, %v", exit, err)
			}
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEmptyRootView(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	spec := f.spec(nil, "empty")
	spec.World, spec.Shim, spec.Process.Dir = nil, Shim{}, "/.oac/home"
	// The launcher inherits the umask; it must not narrow the empty root.
	umask := unix.Umask(0o077)
	v, err := Start(context.Background(), spec)
	unix.Umask(umask)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	if v.Relay() != nil {
		t.Error("an empty root without a shim has a relay")
	}
	out, err := io.ReadAll(v.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if exit, err := v.Wait(); err != nil || exit != (Exit{}) {
		t.Fatalf("Wait = %+v, %v; output %s", exit, err, out)
	}
}

func TestStartRejectsInvalidSpec(t *testing.T) {
	for name, spec := range map[string]Spec{
		"relative overlay":            {Overlays: []Overlay{{Path: "etc/resolv.conf", Source: "/etc/hosts"}}},
		"writable executable private": {Private: []PrivateDir{{Name: "home", HostDir: t.TempDir(), Writable: true, Exec: true}}},
		"missing staging parent":      {StagingParent: filepath.Join(t.TempDir(), "missing")},
		"private run directory":       {Private: []PrivateDir{{Name: "run", HostDir: t.TempDir()}}},
		"shim named as the relay":     {Shim: Shim{Binary: "/bin/true", Names: []string{processshim.RelayName}}},
		"relative cgroup parent":      {CgroupParent: "sys/fs/cgroup/oac"},
	} {
		if spec.StagingParent == "" {
			spec.StagingParent = t.TempDir()
		}
		if spec.CgroupParent == "" {
			spec.CgroupParent = "/sys/fs/cgroup/oac"
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
	self, world, harness, home, overlay, staging, cgroups string
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
		overlay: filepath.Join(base, "overlay"),
		staging: filepath.Join(base, "staging"),
		cgroups: sessionviewtest.CgroupParent(t),
	}
	for _, d := range []string{".oac/harness", ".oac/home", ".oac/run", ".oac/bin", "proc", "sys", "dev", "bin", "usr/bin", "data", "etc/oac-overlay"} {
		mkdir(t, filepath.Join(f.world, d))
	}
	writeFile(t, filepath.Join(f.world, "bin", "sh"), "")
	writeFile(t, filepath.Join(f.world, "usr", "bin", "env"), "")
	writeFile(t, filepath.Join(f.world, "data", "in.txt"), "hello from the world")
	copyFile(t, self, filepath.Join(f.world, "bin", "worldbin"))
	mkdir(t, f.harness)
	copyFile(t, self, filepath.Join(f.harness, "harness"))
	mkdir(t, f.home)
	if err := os.Chown(f.home, viewID, viewID); err != nil {
		t.Fatal(err)
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
		CgroupParent:  f.cgroups,
	}
}

// loopbackWorld serves a directory as the world, the way the world frontend serves a sandbox. It presents each mountpoint at its declared path.
type loopbackWorld struct {
	dir    string
	served chan struct{}
}

func (w *loopbackWorld) serve(_ context.Context, dev *os.File, mount WorldMount) (WorldServer, Presentation, error) {
	root, err := gofs.NewLoopbackRoot(w.dir)
	if err != nil {
		return nil, Presentation{}, err
	}
	return w.serveRoot(root, dev, mount)
}

func (w *loopbackWorld) serveRoot(root gofs.InodeEmbedder, dev *os.File, mount WorldMount) (WorldServer, Presentation, error) {
	fd, err := unix.Dup(int(dev.Fd()))
	if err != nil {
		return nil, Presentation{}, err
	}
	srv, err := fuse.NewServer(gofs.NewNodeFS(root, &gofs.Options{}), fmt.Sprintf("/dev/fd/%d", fd), &fuse.MountOptions{})
	if err != nil {
		unix.Close(fd)
		return nil, Presentation{}, err
	}
	w.served = make(chan struct{})
	go func() {
		srv.Serve()
		close(w.served)
	}()
	var p Presentation
	for _, m := range mount.Mountpoints {
		p.Targets = append(p.Targets, m.Path)
	}
	return w, p, nil
}

func (w *loopbackWorld) Stop() error {
	select {
	case <-w.served:
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("world still serving 10s after the view ended")
	}
}

// hangWorld is a loopbackWorld in which writes to a file named hang get no answer, even when the writer is killed, and Stop never returns, until release closes.
type hangWorld struct {
	loopbackWorld
	hung, release chan struct{} // hung closes at the first write to hang
	hungOnce      sync.Once
}

func (w *hangWorld) serve(_ context.Context, dev *os.File, mount WorldMount) (WorldServer, Presentation, error) {
	root, err := gofs.NewLoopbackRoot(w.dir)
	if err != nil {
		return nil, Presentation{}, err
	}
	root.(*gofs.LoopbackNode).RootData.NewNode = func(r *gofs.LoopbackRoot, _ *gofs.Inode, name string, _ *syscall.Stat_t) gofs.InodeEmbedder {
		n := &gofs.LoopbackNode{RootData: r}
		if name == "hang" {
			return &hangNode{LoopbackNode: n, w: w}
		}
		return n
	}
	_, p, err := w.serveRoot(root, dev, mount)
	if err != nil {
		return nil, p, err
	}
	return w, p, nil
}

func (w *hangWorld) Stop() error {
	<-w.release
	return w.loopbackWorld.Stop()
}

type hangNode struct {
	*gofs.LoopbackNode
	w *hangWorld
}

func (n *hangNode) Write(context.Context, gofs.FileHandle, []byte, int64) (uint32, syscall.Errno) {
	n.w.hungOnce.Do(func() { close(n.w.hung) })
	<-n.w.release
	return 0, syscall.EIO
}

// stallWorld is a loopbackWorld that answers no lookup of name until unstall or Stop.
type stallWorld struct {
	loopbackWorld
	name                   string
	stalled, release       chan struct{} // stalled closes at the first lookup of name
	stallOnce, releaseOnce sync.Once
}

func (w *stallWorld) unstall() { w.releaseOnce.Do(func() { close(w.release) }) }

func (w *stallWorld) serve(_ context.Context, dev *os.File, mount WorldMount) (WorldServer, Presentation, error) {
	root, err := gofs.NewLoopbackRoot(w.dir)
	if err != nil {
		return nil, Presentation{}, err
	}
	root.(*gofs.LoopbackNode).RootData.NewNode = func(r *gofs.LoopbackRoot, _ *gofs.Inode, name string, _ *syscall.Stat_t) gofs.InodeEmbedder {
		if name == w.name {
			w.stallOnce.Do(func() { close(w.stalled) })
			<-w.release
		}
		return &gofs.LoopbackNode{RootData: r}
	}
	_, p, err := w.serveRoot(root, dev, mount)
	if err != nil {
		return nil, p, err
	}
	return w, p, nil
}

func (w *stallWorld) Stop() error {
	w.unstall()
	return w.loopbackWorld.Stop()
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

// pidsWith lists the processes whose command line contains token.
func pidsWith(t *testing.T, token string) []int {
	t.Helper()
	cmdlines, err := filepath.Glob("/proc/[0-9]*/cmdline")
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, p := range cmdlines {
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte(token)) {
			pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(p)))
			pids = append(pids, pid)
		}
	}
	return pids
}

// stat returns the fields of pid's stat after its command name, which may hold anything: the state, the parent's pid and the process group come first. It returns nil once pid is gone.
func stat(pid int) []string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil
	}
	return strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
}

// zombies returns the children of pid that have exited and are not reaped yet.
func zombies(t *testing.T, pid int) []int {
	t.Helper()
	stats, err := filepath.Glob("/proc/[0-9]*/stat")
	if err != nil {
		t.Fatal(err)
	}
	var z []int
	for _, p := range stats {
		child, _ := strconv.Atoi(filepath.Base(filepath.Dir(p)))
		if f := stat(child); len(f) > 1 && f[0] == "Z" && f[1] == strconv.Itoa(pid) {
			z = append(z, child)
		}
	}
	return z
}

func fdCount(t *testing.T, pid int) int {
	t.Helper()
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		t.Fatal(err)
	}
	return len(fds)
}

// inSpawn counts the goroutines in View.Spawn.
func inSpawn() int {
	buf := make([]byte, 1<<20)
	return strings.Count(string(buf[:runtime.Stack(buf, true)]), "sessionview.(*View).Spawn(")
}

// startStalled starts a view whose process waits for TERM and whose world answers no lookup of /data/stall until w.unstall.
func startStalled(t *testing.T) (*View, *stallWorld) {
	t.Helper()
	requireView(t)
	f := newFixture(t)
	mkdir(t, filepath.Join(f.world, "data", "stall"))
	w := &stallWorld{loopbackWorld: loopbackWorld{dir: f.world}, name: "stall", stalled: make(chan struct{}), release: make(chan struct{})}
	spec := f.spec(&w.loopbackWorld, "wait", "OAC_VIEW_TOKEN=oac-unused")
	spec.World = w.serve
	v, err := Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { v.Close() })
	if line, err := bufio.NewReader(v.Stdout()).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("harness said %q, %v", line, err)
	}
	return v, w
}

// spawnHelper spawns this binary as helper mode in dir, with args after its name and no stdin.
func spawnHelper(ctx context.Context, v *View, mode, dir string, args ...string) (*Spawned, error) {
	return v.Spawn(ctx, "/.oac/harness/harness", append([]string{"harness"}, args...), []string{helperEnv + "=" + mode}, dir, false)
}

type spawnResult struct {
	s   *Spawned
	err error
}

func spawnAsync(ctx context.Context, v *View, mode, dir string, args ...string) <-chan spawnResult {
	ch := make(chan spawnResult, 1)
	go func() {
		s, err := spawnHelper(ctx, v, mode, dir, args...)
		ch <- spawnResult{s, err}
	}()
	return ch
}

// waitFor delivers s's exit code, or -2 when Wait fails.
func waitFor(s *Spawned) <-chan int {
	ch := make(chan int, 1)
	go func() {
		code, err := s.Wait()
		if err != nil {
			code = -2
		}
		ch <- code
	}()
	return ch
}

func closeStdio(s *Spawned) { closeFiles([]*os.File{s.Stdin, s.Stdout, s.Stderr}) }

// await returns what ch delivers, failing the test when nothing comes within 10 seconds.
func await[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: nothing within 10s", what)
	}
	var zero T
	return zero
}

// eventually polls cond, failing the test when it does not hold within 10 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s not within 10s", what)
		}
	}
}

// identity describes what this process runs as and in: its credentials and restrictions, its namespaces, its cgroup and its root.
func identity() (map[string]string, error) {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return nil, err
	}
	id := map[string]string{}
	for _, line := range strings.Split(string(status), "\n") {
		k, v, _ := strings.Cut(line, ":")
		switch k {
		case "Uid", "Gid", "Groups", "CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb", "NoNewPrivs", "Seccomp", "Seccomp_filters":
			id[k] = strings.TrimSpace(v)
		}
	}
	for _, ns := range []string{"mnt", "net", "pid", "ipc", "uts", "user", "cgroup"} {
		if id["ns "+ns], err = os.Readlink("/proc/thread-self/ns/" + ns); err != nil {
			return nil, err
		}
	}
	cgroup, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, err
	}
	id["cgroup"] = string(cgroup)
	var st unix.Stat_t
	if err := unix.Stat("/", &st); err != nil {
		return nil, err
	}
	id["root"] = fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	return id, nil
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
		child.Env = []string{helperEnv + "=slow-term", "OAC_VIEW_TOKEN=" + os.Getenv("OAC_VIEW_TOKEN")}
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		<-sigs
		return 7
	case "slow-term":
		// A second TERM ends it before the cleanup finishes.
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM)
		fmt.Println("ready")
		<-sigs
		signal.Reset(syscall.SIGTERM)
		time.Sleep(300 * time.Millisecond)
		if err := os.WriteFile("/data/cleaned"+os.Getenv("OAC_VIEW_TOKEN"), []byte("done"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	case "cwd":
		wd, err := os.Getwd()
		if err == nil {
			err = os.WriteFile("/.oac/home/started", nil, 0o600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Print(wd)
		return 0
	case "noop":
		return 0
	case "empty":
		var errs []error
		var st unix.Statfs_t
		if err := unix.Statfs("/", &st); err != nil || st.Type != unix.TMPFS_MAGIC || st.Flags&(unix.ST_RDONLY|unix.ST_NOEXEC) != unix.ST_RDONLY|unix.ST_NOEXEC {
			errs = append(errs, fmt.Errorf("root: type %#x flags %#x, %v", st.Type, st.Flags, err))
		}
		if err := systemFilesystems(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for dir, want := range map[string][]string{"/": {".oac", "dev", "etc", "proc", "sys"}, "/.oac": {"bin", "harness", "home"}, "/.oac/bin": nil, "/etc": {"oac-overlay"}} {
			entries, err := os.ReadDir(dir)
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			if err != nil || !slices.Equal(names, want) {
				errs = append(errs, fmt.Errorf("%s holds %v, %v", dir, names, err))
			}
		}
		if err := os.WriteFile("/x", nil, 0o644); !errors.Is(err, syscall.EROFS) {
			errs = append(errs, fmt.Errorf("write /x: %v, want EROFS", err))
		}
		if wd, err := os.Getwd(); wd != "/.oac/home" || err != nil {
			errs = append(errs, fmt.Errorf("cwd %q, %v", wd, err))
		}
		errs = append(errs, fileHas("/etc/oac-overlay/greeting", "from the overlay"))
		if err := errors.Join(errs...); err != nil {
			fmt.Println(err)
			return 1
		}
		return 0
	case "identity":
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM)
		id, err := identity()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		json.NewEncoder(os.Stdout).Encode(id)
		<-sigs
		return 7
	case "report":
		in, err := io.ReadAll(os.Stdin)
		wd, werr := os.Getwd()
		data, rerr := os.ReadFile("in.txt")
		fmt.Printf("%s %s %s %v", in, wd, data, errors.Join(err, werr, rerr))
		fmt.Fprint(os.Stderr, "to stderr")
		return 3
	case "hang":
		f, err := os.OpenFile("/data/hang", os.O_WRONLY, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		f.Write([]byte("never answered"))
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
			if pid, err := strconv.Atoi(e.Name()); err == nil && pid != relayPID() {
				pids = append(pids, pid)
			}
		}
		if !slices.Equal(pids, []int{1, os.Getpid()}) {
			return fmt.Errorf("pids %v besides the relay", pids)
		}
		return nil
	}},
	{"system filesystems belong to the view", systemFilesystems},
	{"the relay runs as the view user without privileges", func() error {
		pid := relayPID()
		if pid == 0 {
			return errors.New("no relay")
		}
		id := fmt.Sprintf("%d\t%[1]d\t%[1]d\t%[1]d", viewID)
		return statusHas(fmt.Sprintf("/proc/%d/status", pid), map[string]string{"Uid": id, "Gid": id, "CapPrm": noCaps, "CapEff": noCaps, "CapBnd": noCaps, "NoNewPrivs": "1"})
	}},
	{"the relay's socket accepts the view user and cannot be replaced", func() error {
		c, err := net.Dial("unix", processshim.SocketPath)
		if err != nil {
			return err
		}
		c.Close()
		if err := os.Remove(processshim.SocketPath); !errors.Is(err, syscall.EROFS) {
			return fmt.Errorf("remove: %v, want EROFS", err)
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
// relayPID returns the pid of the view's relay, or 0.
func relayPID() int {
	want := []byte(strings.Join(processshim.RelayArgs, "\x00") + "\x00")
	cmdlines, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, p := range cmdlines {
		if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, want) {
			pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(p)))
			return pid
		}
	}
	return 0
}

func systemFilesystems() error {
	for path, kind := range map[string]int64{"/sys": unix.SYSFS_MAGIC, "/sys/fs/cgroup": unix.CGROUP2_SUPER_MAGIC} {
		var stat unix.Statfs_t
		if err := unix.Statfs(path, &stat); err != nil {
			return err
		}
		if stat.Type != kind || stat.Flags&unix.ST_RDONLY == 0 || stat.Flags&unix.ST_NOEXEC == 0 {
			return fmt.Errorf("%s is not the read-only, noexec kernel filesystem: type=%x flags=%x", path, stat.Type, stat.Flags)
		}
	}
	if err := fileHas("/proc/self/cgroup", "0::/\n"); err != nil {
		return err
	}
	procs, err := os.ReadFile("/sys/fs/cgroup/cgroup.procs")
	if err != nil || !slices.Contains(strings.Fields(string(procs)), strconv.Itoa(os.Getpid())) {
		return fmt.Errorf("current cgroup omits self: %q, %v", procs, err)
	}
	entries, err := os.ReadDir("/sys/fs/cgroup")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("another cgroup is visible: %s", entry.Name())
		}
	}
	devices, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return err
	}
	for _, device := range devices {
		if device.Type()&os.ModeSymlink != 0 && device.Name() != "lo" {
			return fmt.Errorf("another network namespace's device is visible: %s", device.Name())
		}
	}
	for _, path := range []string{"/sys/fs/cgroup/cgroup.procs", "/sys/devices/system/cpu/online"} {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if f != nil {
			f.Close()
			return fmt.Errorf("system file is writable: %s", path)
		}
		if !errors.Is(err, syscall.EROFS) && !errors.Is(err, syscall.EACCES) {
			return fmt.Errorf("write system file %s: %v", path, err)
		}
	}
	return nil
}

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

const noCaps = "0000000000000000"

func noPrivileges() error {
	return statusHas("/proc/self/status", map[string]string{"CapInh": noCaps, "CapPrm": noCaps, "CapEff": noCaps, "CapBnd": noCaps, "CapAmb": noCaps, "NoNewPrivs": "1"})
}

// statusHas checks fields of a /proc/<pid>/status file.
func statusHas(path string, want map[string]string) error {
	status, err := os.ReadFile(path)
	if err != nil {
		return err
	}
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
