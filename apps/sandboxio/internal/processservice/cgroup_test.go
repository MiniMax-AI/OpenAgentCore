//go:build linux

package processservice

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Run the test binary in a delegated cgroup, for example with systemd-run
// --user --property=Delegate=yes. The gate makes missing delegation a failure
// in the qualification job rather than silently skipping that coverage.
func cgroupHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, DefaultConfig())
	if !slices.Contains(h.svc.caps.Scopes, sp.ScopeCgroupV2) {
		if os.Getenv("OAC_TEST_PROCESS_CGROUP") == "1" {
			t.Fatal("a writable cgroup v2 delegation with cgroup.kill is required")
		}
		t.Skip("run in a delegated cgroup with OAC_TEST_PROCESS_CGROUP=1")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.svc.Shutdown(ctx)
	})
	return h
}

func cgroupSpec(argv ...string) sp.ProcessSpec {
	s := pipeSpec(argv...)
	s.Scope = sp.ScopeCgroupV2
	return s
}

func awaitFile(t *testing.T, path string, want string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		b, err := os.ReadFile(path)
		if err == nil && string(b) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("file %s did not reach %q: %q %v", path, want, b, err)
		}
	}
}

func cgroupRecord(t *testing.T, h *harness, op *sp.Operation) *operation {
	t.Helper()
	h.svc.mu.Lock()
	defer h.svc.mu.Unlock()
	return h.svc.ops[opKey{h.att, op.Ref().OperationID}]
}

// The descendant writes only after the observer opens the gate. Cancelling
// its cgroup before that gate must make any later effect impossible, even
// after both its original process group and parent have gone.
func TestCgroupCancelDetachedDescendants(t *testing.T) {
	for _, kind := range []string{"fork", "setsid", "double-fork"} {
		for _, leaderExit := range []bool{false, true} {
			t.Run(kind+"/leader-exited="+strconv.FormatBool(leaderExit), func(t *testing.T) {
				h := cgroupHarness(t)
				dir := t.TempDir()
				writer := filepath.Join(dir, "writer")
				script := "#!/bin/sh\ntrap '' TERM HUP\necho $$ > \"$1/pid\"\nprintf a > \"$1/ticks\"\nwhile [ ! -e \"$1/gate\" ]; do sleep .01; done\nprintf b >> \"$1/ticks\"\n"
				if err := os.WriteFile(writer, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				launch := `"$1" "$2" &`
				if kind == "setsid" {
					launch = `setsid "$1" "$2" &`
				}
				if kind == "double-fork" {
					launch = `(setsid "$1" "$2" &) ;`
				}
				if leaderExit {
					launch += " exit 0"
				} else {
					launch += " while :; do sleep 1; done"
				}
				c := h.connect()
				op := h.start(c, cgroupSpec("sh", "-c", launch, "leader", writer, dir))
				awaitFile(t, filepath.Join(dir, "ticks"), "a")
				raw, err := os.ReadFile(filepath.Join(dir, "pid"))
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
				if err != nil {
					t.Fatal(err)
				}
				fd, err := unix.PidfdOpen(pid, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer unix.Close(fd)
				if leaderExit {
					events(t, op, sp.EventExited)
				} else {
					events(t, op, sp.EventStarted)
				}
				if st, err := op.Inspect(t.Context()); err != nil || st.Scope != sp.ScopeStateActive {
					t.Fatalf("background scope was not retained: %+v %v", st, err)
				}
				record := cgroupRecord(t, h, op)
				group := record.cgroup.path
				// Losing and replacing the observer does not cancel background work.
				c.Close()
				c = h.connect()
				op, _, err = c.Attach(t.Context(), h.svc.instance, op.Ref().OperationID, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := op.Cancel(t.Context(), 0); err != nil {
					t.Fatal(err)
				}
				events(t, op, sp.EventScopeClosed)
				if _, err := os.Stat(group); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("closed group remains: %v", err)
				}
				if st, err := readStat(pid); err == nil && st.live() {
					t.Fatalf("detached writer remains live: %+v", st)
				}
				if err := os.WriteFile(filepath.Join(dir, "gate"), []byte("go"), 0600); err != nil {
					t.Fatal(err)
				}
				awaitFile(t, filepath.Join(dir, "ticks"), "a")
			})
		}
	}
}

func TestCgroupBackgroundCompletesAfterLeader(t *testing.T) {
	h := cgroupHarness(t)
	dir := t.TempDir()
	op := h.start(h.connect(), cgroupSpec("sh", "-c", `(while [ ! -e "$0/gate" ]; do sleep .01; done; printf completed > "$0/result") & exit 0`, dir))
	events(t, op, sp.EventExited)
	wantCode(t, op.Release(t.Context()), sp.CodeBusy)
	if st, err := op.Inspect(t.Context()); err != nil || st.Scope != sp.ScopeStateActive {
		t.Fatalf("scope %+v %v", st, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gate"), []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	events(t, op, sp.EventScopeClosed)
	awaitFile(t, filepath.Join(dir, "result"), "completed")
}

func TestCgroupFailedLaunchLeavesNoScope(t *testing.T) {
	h := cgroupHarness(t)
	op := h.start(h.connect(), cgroupSpec("no-such-command"))
	ev := next(t, op)
	if f, ok := ev.(sp.StartFailedEvent); !ok || f.Failure.Code != sp.CodeNotFound {
		t.Fatalf("first event: %+v", ev)
	}
	record := cgroupRecord(t, h, op)
	if _, err := os.Stat(record.cgroup.path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed launch group remains: %v", err)
	}
	if err := op.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCgroupCancelWhileStarting(t *testing.T) {
	h := cgroupHarness(t)
	spec := cgroupSpec("sh", "-c", "sleep 30 & wait")
	key := opKey{h.att, sandboxwire.NewID()}
	op := newOperation(h.svc, key, spec.Digest())
	h.svc.mu.Lock()
	h.svc.ops[key] = op
	h.svc.active.Add(1)
	h.svc.mu.Unlock()
	if err := op.cancel(0); err != nil {
		t.Fatal(err)
	}
	op.launch(sp.StartRequest{OperationRef: sp.OperationRef{ServerInstanceID: h.svc.instance, OperationID: key.operation}, Spec: spec})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	op.awaitScope(ctx)
	if ctx.Err() != nil || op.inspect().Scope != sp.ScopeStateClosed {
		t.Fatalf("pending cancellation did not settle: %+v %v", op.inspect(), ctx.Err())
	}
	if _, err := os.Stat(op.cgroup.path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cancelled group remains: %v", err)
	}
}

func TestCgroupCancelDoesNotReachOtherOperation(t *testing.T) {
	h := cgroupHarness(t)
	c := h.connect()
	first := h.start(c, cgroupSpec("sleep", "30"))
	other := h.start(c, cgroupSpec("sleep", "30"))
	events(t, first, sp.EventStarted)
	events(t, other, sp.EventStarted)
	if err := first.Cancel(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	events(t, first, sp.EventScopeClosed)
	if st, err := other.Inspect(t.Context()); err != nil || st.State != sp.StateRunning || st.Scope != sp.ScopeStateActive {
		t.Fatalf("other operation changed: %+v %v", st, err)
	}
	if err := other.Cancel(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	events(t, other, sp.EventScopeClosed)
}

func TestCgroupPlacementAndGracefulSignal(t *testing.T) {
	h := cgroupHarness(t)
	op := h.start(h.connect(), cgroupSpec("sh", "-c", `trap 'printf stopped; exit 0' TERM; cat /proc/self/cgroup; printf ready; while :; do sleep 1; done`))
	var before []sp.Event
	for !strings.Contains(output(before, sp.StreamStdout), "ready") {
		before = append(before, next(t, op))
	}
	group := filepath.Base(cgroupRecord(t, h, op).cgroup.path)
	if !strings.Contains(output(before, sp.StreamStdout), "/"+group+"\n") {
		t.Fatalf("target started outside its cgroup: %q", output(before, sp.StreamStdout))
	}
	if err := op.Signal(t.Context(), 15, sp.TargetScope); err != nil {
		t.Fatal(err)
	}
	evs := events(t, op, sp.EventScopeClosed)
	exit, _ := find[sp.ExitedEvent](t, evs)
	if exit.Status.Kind != sp.ExitCode || exit.Status.Code != 0 {
		t.Fatalf("TERM handler did not finish: %+v", exit.Status)
	}
	if !strings.Contains(output(evs, sp.StreamStdout), "stopped") {
		t.Fatalf("TERM handler output missing: %q", output(evs, sp.StreamStdout))
	}
}

func TestCgroupObservationFailureDoesNotSettle(t *testing.T) {
	h := cgroupHarness(t)
	dir := t.TempDir()
	op := h.start(h.connect(), cgroupSpec("sh", "-c", `(while [ ! -e "$0/gate" ]; do sleep .01; done) & exit 0`, dir))
	group := cgroupRecord(t, h, op).cgroup.path
	eventsFile := filepath.Join(group, "cgroup.events")
	if err := os.Chmod(eventsFile, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(eventsFile, 0444) })
	evs := events(t, op, sp.EventObservationLost)
	lost, _ := find[sp.ObservationLostEvent](t, evs)
	if lost.Observation != sp.ObservationScope {
		t.Fatalf("wrong lost observation: %+v", lost)
	}
	if st, err := op.Inspect(t.Context()); err != nil || st.Scope != sp.ScopeStateUnknown {
		t.Fatalf("scope %+v %v", st, err)
	}
	wantCode(t, op.Release(t.Context()), sp.CodeBusy)
	if err := os.Chmod(eventsFile, 0444); err != nil {
		t.Fatal(err)
	}
	if err := op.Cancel(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	events(t, op, sp.EventScopeClosed)
}

func TestCgroupOutputDrainsAfterLeader(t *testing.T) {
	h := cgroupHarness(t)
	dir := t.TempDir()
	op := h.start(h.connect(), cgroupSpec("sh", "-c", `printf leader; (while [ ! -e "$0/gate" ]; do sleep .01; done; printf child) & exit 0`, dir))
	evs := events(t, op, sp.EventExited)
	if output(evs, sp.StreamStdout) != "leader" {
		t.Fatalf("leader output: %q", output(evs, sp.StreamStdout))
	}
	if err := os.WriteFile(filepath.Join(dir, "gate"), []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	scope, drained := false, false
	for !scope || !drained {
		ev := next(t, op)
		evs = append(evs, ev)
		switch ev.(type) {
		case sp.ScopeClosedEvent:
			scope = true
		case sp.OutputClosedEvent:
			drained = true
		}
	}
	if output(evs, sp.StreamStdout) != "leaderchild" {
		t.Fatalf("lost descendant output: %q", output(evs, sp.StreamStdout))
	}
	if err := op.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// A launch that has already failed still owns its scope until observation
// confirms it empty. A filesystem error must not expose an invalid status,
// claim settlement, or prevent other requests and bounded Shutdown.
func TestCgroupFailedLaunchObservationAndShutdown(t *testing.T) {
	h := cgroupHarness(t)
	spec := cgroupSpec("no-such-command")
	key := opKey{h.att, sandboxwire.NewID()}
	record := newOperation(h.svc, key, spec.Digest())
	h.svc.mu.Lock()
	h.svc.ops[key] = record
	h.svc.active.Add(1)
	h.svc.mu.Unlock()
	_, failure := record.spawn(sp.StartRequest{OperationRef: sp.OperationRef{ServerInstanceID: h.svc.instance, OperationID: key.operation}, Spec: spec})
	if failure == nil {
		t.Fatal("launch unexpectedly succeeded")
	}
	eventsFile := filepath.Join(record.cgroup.path, "cgroup.events")
	if err := os.Chmod(eventsFile, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(eventsFile, 0444) })
	record.mu.Lock()
	record.startFailure = failure
	record.killing = true
	record.mu.Unlock()
	go record.watchScope()
	c := h.connect()
	op, _, err := c.Attach(t.Context(), h.svc.instance, key.operation, 0)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		st, err := op.Inspect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Validate(); err != nil {
			t.Fatal(err)
		}
		if st.Scope == sp.ScopeStateUnknown {
			if st.State != sp.StateStarting || st.StartFailure != nil {
				t.Fatalf("invalid pending failure: %+v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("observation failure was not visible")
		}
	}
	select {
	case ev := <-op.Events():
		t.Fatalf("event before confirmed failure: %+v", ev)
	default:
	}
	other := h.start(c, cgroupSpec("true"))
	events(t, other, sp.EventScopeClosed)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	done := make(chan struct{})
	go func() { h.svc.Shutdown(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = os.Chmod(eventsFile, 0444)
		cancel()
		t.Fatal("shutdown ignored its deadline")
	}
	err = ctx.Err()
	cancel()
	if err != context.DeadlineExceeded {
		t.Fatalf("shutdown returned without its unresolved scope: %v", err)
	}
	if err := os.Chmod(eventsFile, 0444); err != nil {
		t.Fatal(err)
	}
	ev := next(t, op)
	if _, ok := ev.(sp.StartFailedEvent); !ok {
		t.Fatalf("wrong first event: %+v", ev)
	}
	if st, err := op.Inspect(t.Context()); err != nil || st.State != sp.StateStartFailed || st.Scope != sp.ScopeStateClosed {
		t.Fatalf("failure not settled: %+v %v", st, err)
	}
}

func TestCgroupEmptyDirectoryFailureDoesNotKeepScopeActive(t *testing.T) {
	h := cgroupHarness(t)
	g, err := newProcessCgroup(h.svc.cgroupParent)
	if err != nil {
		t.Fatal(err)
	}
	// Inject a directory removal failure after the kernel confirms emptiness.
	// Cleanup failure must not turn an empty process scope into Unknown.
	original := g.path
	g.path = filepath.Join(original, "missing-parent", "group")
	if live, err := g.observe(); live != 0 || err != nil {
		t.Fatalf("empty group: %d %v", live, err)
	}
	if err := g.cleanup(); err == nil {
		t.Fatal("injected directory removal failure was hidden")
	}
	if live, err := g.observe(); live != 0 || err != nil {
		t.Fatalf("cleanup error changed empty scope: %d %v", live, err)
	}
	g.path = original
	if err := g.cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestCgroupKillFailureKeepsNestedScopeActive(t *testing.T) {
	h := cgroupHarness(t)
	dir := t.TempDir()
	script := `trap '' TERM HUP
group=/sys/fs/cgroup$(sed -n 's/^0:://p' /proc/self/cgroup)
mkdir "$group/nested"
echo $$ > "$group/nested/cgroup.procs"
(trap '' TERM HUP; printf a > "$0/ticks"; while [ ! -e "$0/gate" ]; do sleep .01; done; printf b >> "$0/ticks"; while :; do sleep 1; done) &
while :; do sleep 1; done`
	op := h.start(h.connect(), cgroupSpec("sh", "-c", script, dir))
	awaitFile(t, filepath.Join(dir, "ticks"), "a")
	events(t, op, sp.EventStarted)
	group := cgroupRecord(t, h, op).cgroup.path
	killFile := filepath.Join(group, "cgroup.kill")
	if err := os.Chmod(killFile, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(killFile, 0200)
		_ = cgroupRecord(t, h, op).cgroup.signal(unix.SIGKILL)
	})
	wantCode(t, op.Signal(t.Context(), 9, sp.TargetScope), sp.CodeIO)
	if err := op.Cancel(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gate"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitFile(t, filepath.Join(dir, "ticks"), "ab")
	if st, err := op.Inspect(t.Context()); err != nil || st.Scope != sp.ScopeStateActive {
		t.Fatalf("failed kill falsely closed scope: %+v %v", st, err)
	}
	wantCode(t, op.Release(t.Context()), sp.CodeBusy)
	if err := os.Chmod(killFile, 0200); err != nil {
		t.Fatal(err)
	}
	// The existing cancellation poll retries the failed KILL without a new Cancel.
	events(t, op, sp.EventScopeClosed)
	if _, err := os.Stat(group); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("nested scope was not removed: %v", err)
	}
}

func TestCgroupDelegationRequiresParentMigrationPermission(t *testing.T) {
	h := cgroupHarness(t)
	procs := filepath.Join(h.svc.cgroupParent, "cgroup.procs")
	info, err := os.Stat(procs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(procs, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(procs, info.Mode().Perm()) })
	service, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(service.caps.Scopes, sp.ScopeCgroupV2) {
		t.Fatal("advertised cgroup scope without parent migration permission")
	}
}

func TestCgroupKillEmptyReturnsNotRunning(t *testing.T) {
	h := cgroupHarness(t)
	group, err := newProcessCgroup(h.svc.cgroupParent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = group.observe(); _ = group.cleanup() })
	wantCode(t, group.signal(unix.SIGKILL), sp.CodeNotRunning)
}

func TestCgroupKillWithUnknownMembershipStillStopsProcesses(t *testing.T) {
	h := cgroupHarness(t)
	dir := t.TempDir()
	op := h.start(h.connect(), cgroupSpec("sh", "-c", `trap '' TERM HUP; printf ready > "$0/ready"; while :; do sleep 1; done`, dir))
	awaitFile(t, filepath.Join(dir, "ready"), "ready")
	group := cgroupRecord(t, h, op).cgroup.path
	eventsFile := filepath.Join(group, "cgroup.events")
	if err := os.Chmod(eventsFile, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(eventsFile, 0444) })
	// Unknown membership cannot prove a successful Signal, but must not
	// prevent the cgroup kill from stopping processes whose observation failed.
	wantCode(t, op.Signal(t.Context(), 9, sp.TargetScope), sp.CodeIO)
	awaitFile(t, filepath.Join(group, "cgroup.procs"), "")
	events(t, op, sp.EventObservationLost)
	if st, err := op.Inspect(t.Context()); err != nil || st.Scope != sp.ScopeStateUnknown {
		t.Fatalf("unknown scope was falsely settled: %+v %v", st, err)
	}
	wantCode(t, op.Release(t.Context()), sp.CodeBusy)
	if err := os.Chmod(eventsFile, 0444); err != nil {
		t.Fatal(err)
	}
	events(t, op, sp.EventScopeClosed)
}
