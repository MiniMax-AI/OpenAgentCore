//go:build linux

package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processbroker"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview/sessionviewtest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// The view suite needs root with CAP_SYS_ADMIN and CAP_NET_ADMIN, /dev/fuse,
// no AppArmor confinement, a private cgroup namespace, in which
// sessionviewtest mounts a writable cgroup v2 hierarchy for the views, and a
// static oac-sandbox-io, whose world is the container's /. Run it in a
// throwaway container:
//
//	CGO_ENABLED=0 go build -o /tmp/oac-sandbox-io ./apps/sandboxio/cmd/oac-sandbox-io
//	CGO_ENABLED=0 go test -c -o /tmp/agenthost.test ./apps/daemon/internal/agenthost
//	docker run --rm --cgroupns=private --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
//	  -e OAC_TEST_AGENTHOST=1 -e OAC_TEST_SANDBOXIO=/sandboxio -v /tmp/oac-sandbox-io:/sandboxio:ro \
//	  -v /tmp/agenthost.test:/t.test:ro debian:bookworm-slim /t.test -test.v
const (
	gateEnv      = "OAC_TEST_AGENTHOST"
	sandboxIOEnv = "OAC_TEST_SANDBOXIO"
	harnessEnv   = "OAC_AGENTHOST_HARNESS"
	modelEnv     = "OAC_AGENTHOST_MODEL"
	caEnv        = "OAC_AGENTHOST_CA"
	harnessPath  = "/.oac/harness/harness"
	upstreamKey  = "sk-agenthost-upstream"
	wait         = 20 * time.Second
)

func TestSessionRunsInAViewOverItsAttachment(t *testing.T) {
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a privileged container; see the comment above", gateEnv)
	}
	if err := sessionview.Probe(); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	sb := startSandbox(t, os.Getenv(sandboxIOEnv))
	// The model upstream reports whether each request carried the real key.
	keyed := make(chan bool, 4)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyed <- r.Header.Get("X-Api-Key") == upstreamKey
		io.WriteString(w, "answer")
	}))
	defer upstream.Close()

	reg := agent.NewRegistry()
	cfg := newConfig(t, reg, upstream.Certificate())
	cfg.RelayURL = sb.url
	cfg.ViewCgroups = sessionviewtest.CgroupParent(t)
	logged := &failures{}
	cfg.Log = slog.New(logged)
	closure := t.TempDir()
	if err := os.Chmod(closure, 0o755); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, filepath.Join(closure, "harness"))
	register(reg, "test", &agent.View{
		Closure:      []agent.ViewMount{{Name: "harness", HostDir: closure}},
		Masks:        []agent.ViewMask{{Path: "/etc/ld.so.preload"}, {Path: "/etc/hostname"}, {Path: "/etc/apt", Dir: true}},
		LocalExec:    []string{harnessPath},
		ShimPaths:    []string{"/bin/sh"},
		Proxy:        agent.ViewProxyNone,
		Capabilities: declared(proto.CapabilityUnsupported),
		Executor: func(_ context.Context, req proto.PromptRequestPayload, s agent.ViewSession) (agent.Executor, error) {
			return &testExecutor{session: s, dir: req.LocalEnvironment.WorkspaceRoot,
				env: []string{harnessEnv + "=1", modelEnv + "=" + req.ModelProvider.BaseURL, caEnv + "=" + cfg.CADir}}, nil
		},
	})
	sb.auth.AddRuntime(cfg.Credential, cfg.RuntimeID)
	sb.ready(t, cfg)
	h, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { h.Close() }()
	workspace, err := os.MkdirTemp("/tmp", "agenthost-workspace-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
	if err := os.Chmod(workspace, 0o777); err != nil {
		t.Fatal(err)
	}
	req := request("test", workspace, upstream.URL, upstreamKey)
	// Each subtest's daemon drives its Sessions over the relay.
	newRun := func(t *testing.T) *daemon { return newDaemon(t, cfg, deps{dial: relayDial(cfg), tasks: taskUIDs}) }
	beat := filepath.Join(workspace, "beat")
	beating := func() bool {
		_, err := os.Stat(beat)
		return err == nil
	}

	t.Run("one Session", func(t *testing.T) {
		d, b := newRun(t), sb.bind(cfg.RuntimeID, 2*time.Second)
		r := d.turn(t, b, req, "check")
		for _, name := range harnessChecks {
			if msg, ok := r.Checks[name]; !ok || msg != "" {
				t.Errorf("%s: %q", name, msg)
			}
		}
		if r.Exit != "" {
			t.Errorf("Harness: %s; stderr %s", r.Exit, r.Stderr)
		}
		// The Turn waited for its Harness, so no view runs.
		s := d.session(b)
		if _, err := s.spawn(clirunner.StartOptions{Binary: harnessPath, Dir: workspace, OwnProcessGroup: true}); !errors.Is(err, agent.ErrNoLiveView) {
			t.Errorf("Spawn after the Harness exited = %v, want ErrNoLiveView", err)
		}
		if _, err := s.spawn(clirunner.StartOptions{Binary: "/bin/sh", Dir: workspace, OwnProcessGroup: true}); !errors.Is(err, agent.ErrNotLocalExec) {
			t.Errorf("Spawn of a binary outside LocalExec = %v, want ErrNotLocalExec", err)
		}
		select {
		case ok := <-keyed:
			if !ok {
				t.Error("the upstream did not receive the configured key")
			}
		default:
			t.Error("no request reached the upstream")
		}
		if data, err := os.ReadFile(filepath.Join(workspace, "renamed")); err != nil || string(data) != "world" {
			t.Errorf("the renamed world file holds %q, %v", data, err)
		}
		// Only renewal keeps the attachment past its 2 second lease.
		time.Sleep(3 * time.Second)
		if r := d.turn(t, b, req, "touch"); r.Checks["touch"] != "" || r.Exit != "" {
			t.Errorf("touch after the first lease: %+v", r)
		}
		if data, err := os.ReadFile(filepath.Join(workspace, "touched")); err != nil || string(data) != "renewed" {
			t.Errorf("the file written after the first lease holds %q, %v", data, err)
		}
		if err := d.shutdown(); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		if err := sb.renew(t, cfg, s.link.attachment, b); !errors.Is(err, sandboxlink.LeaseExpired) {
			t.Errorf("Renew after Close = %v, want LeaseExpired for a closed attachment", err)
		}
		if err := logged.take(); err != nil {
			t.Errorf("logged %v", err)
		}
		checkReleased(t, cfg)
	})

	t.Run("a command runs in the sandbox through the shim", func(t *testing.T) {
		d, b := newRun(t), sb.bind(cfg.RuntimeID, time.Minute)
		if r := d.turn(t, b, req, "shim"); r.Stdout != "42\n" || r.Code != 3 {
			t.Errorf("the forwarded command printed %q and exited %d, want 42 and 3; stderr %s", r.Stdout, r.Code, r.Stderr)
		}
		if err := d.shutdown(); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		checkReleased(t, cfg)
	})

	t.Run("two Sessions run at once", func(t *testing.T) {
		d, waiting, other := newRun(t), sb.bind(cfg.RuntimeID, time.Minute), sb.bind(cfg.RuntimeID, time.Minute)
		run := d.start(t, waiting, req, "wait")
		defer os.Remove(beat)
		until(t, "the Harness to run", beating)
		// A Subagent runs in the live view.
		p, err := d.session(waiting).spawn(clirunner.StartOptions{Binary: harnessPath, Args: []string{"touch"}, Dir: workspace,
			Env: []string{harnessEnv + "=1"}, OwnProcessGroup: true})
		if err != nil {
			t.Fatalf("Spawn in the live view: %v", err)
		}
		out, _ := io.ReadAll(p.Stdout)
		if err := p.Wait(); err != nil || string(out) != "{\"touch\":\"\"}\n" {
			t.Errorf("the spawned process printed %q and ended with %v", out, err)
		}
		if r := d.turn(t, other, req, "shim"); r.Stdout != "42\n" || r.Code != 3 {
			t.Errorf("the other Session's command printed %q and exited %d; stderr %s", r.Stdout, r.Code, r.Stderr)
		}
		// Cancelling the waiting Turn ends it with its view.
		d.handle(t, proto.TypePromptCancel, run, proto.PromptCancelPayload{})
		d.done(t, run)
		if err := d.shutdown(); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		if err := logged.take(); err != nil {
			t.Errorf("logged %v", err)
		}
		checkReleased(t, cfg)
	})

	t.Run("a lost relay fails the Session", func(t *testing.T) {
		d, b := newRun(t), sb.bind(cfg.RuntimeID, time.Minute)
		run := d.start(t, b, req, "wait")
		defer os.Remove(beat)
		until(t, "the Harness to run", beating)
		relays := processesWith(processshim.RelayArgs)
		if len(relays) != 1 {
			t.Fatalf("%d process relays run, want 1", len(relays))
		}
		if err := syscall.Kill(relays[0], syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		d.done(t, run)
		if err := d.shutdown(); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		if err := logged.take(); !errors.Is(err, ErrProcessBroker) || !errors.Is(err, processbroker.ErrRelayLost) || errors.Is(err, ErrTeardown) {
			t.Errorf("logged %v, want ErrProcessBroker with ErrRelayLost", err)
		}
		checkReleased(t, cfg)
	})

	t.Run("a restarted sandbox service fails the Session", func(t *testing.T) {
		d, b := newRun(t), sb.bind(cfg.RuntimeID, time.Minute)
		run := d.start(t, b, req, "wait")
		defer os.Remove(beat)
		until(t, "the Harness to write to the world", beating)
		sb.stop()
		sb.start(t)
		d.done(t, run)
		if err := d.shutdown(); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		err := logged.take()
		t.Logf("logged %v", err)
		if !errors.Is(err, ErrLink) && !errors.Is(err, ErrWorld) || errors.Is(err, ErrTeardown) {
			t.Errorf("logged %v, want ErrLink or ErrWorld and a complete teardown", err)
		}
		checkReleased(t, cfg)
	})

	t.Run("a home survives a restart", func(t *testing.T) {
		d, b := newRun(t), sb.bind(cfg.RuntimeID, time.Minute)
		if r := d.turn(t, b, req, "check"); r.Checks["home"] != "" {
			t.Fatalf("home: %q", r.Checks["home"])
		}
		if err := d.shutdown(); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		dir := sessionDir(filepath.Join(sessionsDir(cfg.StateDir), b.SessionID.String()))
		owner := func() uint32 {
			var st syscall.Stat_t
			if err := syscall.Stat(dir.entry(homeEntry, "probe"), &st); err != nil {
				t.Fatal(err)
			}
			return st.Uid
		}
		first := owner()
		// An agent host that crashed leaves the transient entries, and a
		// process outside the views holds the first uid.
		for _, name := range []string{etcEntry, maskEntry, stagingEntry} {
			if err := os.Mkdir(dir.entry(name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		holder := exec.Command("/bin/sleep", "60")
		holder.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: first, Gid: first}}
		if err := holder.Start(); err != nil {
			t.Fatal(err)
		}
		h.Close()
		if h, err = Open(cfg); err != nil {
			t.Fatalf("Open after a restart: %v", err)
		}
		if left := leftEntries(t, cfg); len(left) != 0 {
			t.Errorf("Open kept the transient entries %v", left)
		}
		d = newRun(t)
		if r := d.turn(t, b, req, "home"); r.Checks["home kept"] != "" || r.Exit != "" {
			t.Errorf("the reopened Session: %+v", r)
		}
		if err := d.shutdown(); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		if second := owner(); second == first {
			t.Errorf("the reopened Session's home is owned by uid %d, want a fresh uid", second)
		}
		holder.Process.Kill()
		holder.Wait()
		if err := logged.take(); err != nil {
			t.Errorf("logged %v", err)
		}
		checkReleased(t, cfg)
	})

	t.Run("allocation skips a uid that a thread holds under an exited leader", func(t *testing.T) {
		id := cfg.UIDs.First + 1
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), zombieLeaderEnv+"=1")
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: id, Gid: id}}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() {
			cmd.Process.Kill()
			cmd.Wait()
		}()
		until(t, "a zombie leader with a running thread", func() bool { return zombieLeaderHolds(id) })
		if got, err := allocUID(UIDRange{First: id, Count: 1}, taskUIDs); !errors.Is(err, ErrCapacity) {
			freeUID(got)
			t.Errorf("allocUID beside a running thread = %d, %v", got, err)
		}
	})
}

// processesWith lists the processes whose argv is args.
func processesWith(args []string) []int {
	var pids []int
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if cmdline, err := os.ReadFile(procPath(pid, "cmdline")); err == nil && string(cmdline) == strings.Join(args, "\x00")+"\x00" {
			pids = append(pids, pid)
		}
	}
	return pids
}

func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// zombieLeaderHolds reports whether a process whose leader thread is a zombie
// runs a thread whose real uid is id.
func zombieLeaderHolds(id uint32) bool {
	zombie, holding := map[int]bool{}, map[int]bool{}
	for _, t := range threads() {
		switch {
		case t.tid == t.tgid && !t.running:
			zombie[t.tgid] = true
		case t.running && t.uids[0] == id:
			holding[t.tgid] = true
		}
	}
	for tgid := range holding {
		if zombie[tgid] {
			return true
		}
	}
	return false
}

type thread struct {
	tgid, tid int
	uids      [4]uint32
	running   bool
}

// threads lists every thread in /proc, zombies included.
func threads() []thread {
	var list []thread
	pids, _ := os.ReadDir("/proc")
	for _, p := range pids {
		tgid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		tids, _ := os.ReadDir(procPath(tgid, "task"))
		for _, e := range tids {
			tid, _ := strconv.Atoi(e.Name())
			if s, err := readStatus(procPath(tgid, "task", e.Name(), "status")); err == nil {
				list = append(list, thread{tgid: tgid, tid: tid, uids: s.uids, running: s.running()})
			}
		}
	}
	return list
}

// zombieLeaderEnv makes the test binary a process whose leader thread exits
// while another thread runs on.
const zombieLeaderEnv = "OAC_AGENTHOST_ZOMBIE_LEADER"

func runZombieLeader() {
	runtime.LockOSThread()
	started := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		close(started)
		time.Sleep(time.Hour)
	}()
	<-started
	unix.RawSyscall(unix.SYS_EXIT, 0, 0, 0) // ends this thread only
}

// sandbox is a relay and the oac-sandbox-io serving its one resource.
type sandbox struct {
	auth      *sandboxlinktest.Authority
	url       string
	bin       string
	bootstrap string
	resource  sandboxlink.ResourceRef
	cmd       *exec.Cmd
}

func startSandbox(t *testing.T, bin string) *sandbox {
	if bin == "" {
		t.Fatalf("set %s to a static oac-sandbox-io", sandboxIOEnv)
	}
	auth := sandboxlinktest.NewAuthority()
	rl := relay.New(auth)
	srv := httptest.NewServer(rl)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { rl.Close() })
	url := "ws://" + strings.TrimPrefix(srv.URL, "http://")
	in := sandboxbootstrap.Input{Version: sandboxbootstrap.Version, LinkURL: url, Credential: "serve-credential", Resource: sandboxbootstrap.Resource{
		TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), Kind: "allocation", ID: uuid.NewString(), Generation: 1}}
	raw, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(bootstrap, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sb := &sandbox{auth: auth, url: url, bin: bin, bootstrap: bootstrap, resource: in.Resource.Ref()}
	auth.AddServe([]byte(in.Credential), sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: sb.resource})
	sb.start(t)
	t.Cleanup(sb.stop)
	return sb
}

func (sb *sandbox) start(t *testing.T) {
	cmd := exec.Command(sb.bin, "--bootstrap-file", sb.bootstrap)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sb.cmd = cmd
}

func (sb *sandbox) stop() {
	if sb.cmd != nil {
		sb.cmd.Process.Signal(syscall.SIGTERM)
		sb.cmd.Wait()
		sb.cmd = nil
	}
}

// bind returns the binding of a new Session on the resource, granted to
// runtimeID for lease.
func (sb *sandbox) bind(runtimeID sandboxwire.ID, lease time.Duration) Binding {
	b := newBinding(sb.resource)
	sb.auth.AddGrant(b.AttachGrant, sandboxlinktest.Grant{RuntimeID: runtimeID, Resource: b.Resource, SessionID: b.SessionID,
		AssignmentID: b.AssignmentID, AssignmentEpoch: b.AssignmentEpoch, Lease: lease,
		Services: []sandboxlink.Service{sandboxlink.ServiceFile, sandboxlink.ServiceProcess, sandboxlink.ServiceNetwork}})
	return b
}

func (sb *sandbox) dial(t *testing.T, cfg Config) *sandboxlink.AttachLink {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	link, err := sandboxlink.DialAttach(ctx, sandboxlink.AttachConfig{URL: sb.url, RuntimeID: cfg.RuntimeID, Credential: cfg.Credential})
	if err != nil {
		t.Fatal(err)
	}
	return link
}

// ready waits until the relay holds oac-sandbox-io as the resource's serve
// peer, so that an Open reaches it.
func (sb *sandbox) ready(t *testing.T, cfg Config) {
	t.Helper()
	b, attachment := sb.bind(cfg.RuntimeID, time.Minute), sandboxwire.NewID()
	link := sb.dial(t, cfg)
	defer link.Close()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	for {
		st, _, err := link.OpenService(ctx, sandboxlink.Open{Service: sandboxlink.ServiceFile, Version: sandboxfs.Version, Resource: b.Resource,
			AttachmentID: attachment, SessionID: b.SessionID, AssignmentID: b.AssignmentID, AssignmentEpoch: b.AssignmentEpoch, AttachGrant: b.AttachGrant})
		if err == nil {
			st.Close()
			break
		}
		if !errors.Is(err, sandboxlink.ServiceUnavailable) || ctx.Err() != nil {
			t.Fatalf("oac-sandbox-io is not serving: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := link.CloseAttachment(ctx, attachment); err != nil {
		t.Fatal(err)
	}
}

// renew renews b's attachment from a fresh link.
func (sb *sandbox) renew(t *testing.T, cfg Config, attachment sandboxwire.ID, b Binding) error {
	link := sb.dial(t, cfg)
	defer link.Close()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	_, err := link.Renew(ctx, sandboxlink.RenewAttachment{AttachmentID: attachment, AttachGrant: b.AttachGrant})
	return err
}

// turn runs a Turn in mode on b's Session and returns its report.
func (dm *daemon) turn(t *testing.T, b Binding, req proto.PromptRequestPayload, mode string) report {
	t.Helper()
	sent := dm.done(t, dm.start(t, b, req, mode))
	var r report
	if len(sent) != 2 || sent[0].Type != proto.TypeOutputMessage || json.Unmarshal(sent[0].Payload, &r) != nil {
		t.Fatalf("the %s Turn sent %d envelopes, the first a %s, want its report and its Done", mode, len(sent), sent[0].Type)
	}
	return r
}

// checkReleased checks that no transient entry, view cgroup, mount or running
// process with a Session uid remains.
func checkReleased(t *testing.T, cfg Config) {
	t.Helper()
	if left := leftEntries(t, cfg); len(left) != 0 {
		t.Errorf("transient entries remain: %v", left)
	}
	if left := sessionviewtest.Cgroups(t, cfg.ViewCgroups); len(left) != 0 {
		t.Errorf("view cgroups remain: %v", left)
	}
	mounts, err := os.ReadFile("/proc/thread-self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mounts), cfg.StateDir) {
		t.Error("a mount under the state directory remains")
	}
	if held, err := heldUIDs(taskUIDs, cfg.UIDs); err != nil || len(held) != 0 {
		t.Errorf("Session uids held: %v, %v", held, err)
	}
}

func copyExecutable(t *testing.T, dst string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

// testExecutor runs one Harness view per Turn; the Turn's input text is the
// Harness mode.
type testExecutor struct {
	session agent.ViewSession
	dir     string
	env     []string
}

func (e *testExecutor) StartTurn(_ context.Context, runID string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	mode := *input[0].Content[0].Text
	p, err := e.session.Launch(clirunner.StartOptions{Binary: harnessPath, Args: []string{mode}, Dir: e.dir, Env: e.env,
		OwnProcessGroup: true, KillTimeout: time.Second})
	if err != nil {
		return nil, err
	}
	turn := &testTurn{p: p, settled: make(chan struct{})}
	go turn.run(runID, out)
	return turn, nil
}

func (e *testExecutor) Close(context.Context) error { return nil }

// report is a Turn's report envelope: the Harness's checks, or its stdout
// when that is not a check report, its stderr and how it exited.
type report struct {
	Checks map[string]string `json:"checks"`
	Stdout string            `json:"stdout"`
	Stderr string            `json:"stderr"`
	Exit   string            `json:"exit"`
	Code   int               `json:"code"`
}

type testTurn struct {
	p       *clirunner.Process
	settled chan struct{}
}

func (t *testTurn) run(runID string, out chan<- proto.Envelope) {
	defer close(t.settled)
	defer close(out)
	var stderr bytes.Buffer
	copied := make(chan struct{})
	go func() {
		io.Copy(&stderr, t.p.Stderr)
		close(copied)
	}()
	stdout, _ := io.ReadAll(t.p.Stdout)
	<-copied
	var r report
	if json.Unmarshal(stdout, &r.Checks) != nil {
		r.Stdout = string(stdout)
	}
	if err := t.p.Wait(); err != nil {
		r.Exit = err.Error()
	}
	r.Code, _ = t.p.ExitCode()
	r.Stderr = stderr.String()
	payload, _ := json.Marshal(r)
	out <- proto.Envelope{Type: proto.TypeOutputMessage, ID: runID, Payload: payload}
	done, _ := proto.NewEnvelope(proto.TypeDone, runID, proto.DonePayload{})
	out <- done
}

func (t *testTurn) Cancel(context.Context) error {
	t.p.Cancel()
	return nil
}

func (t *testTurn) CancellationOutcome() proto.DonePayload { return proto.DonePayload{} }

func (t *testTurn) SteerWithReceipt(context.Context, proto.PromptSteerPayload, func()) error {
	return agent.ErrUnsupportedOperation
}

func (t *testTurn) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	select {
	case <-t.settled:
		return agent.TurnSettlement{Reusable: true}, nil
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

var harnessChecks = []string{"world rename", "model through the gateway", "no direct route", "world is noexec", "masks", "home", "passwd", "CA directory"}

// runHarness runs inside the view, in the workspace, and prints a JSON map
// from each check to its failure, empty when it passed.
func runHarness(args []string) int {
	if len(args) != 1 {
		return 2
	}
	checks := map[string]func() error{}
	switch args[0] {
	case "check":
		checks = map[string]func() error{
			"world rename": func() error {
				if err := os.WriteFile("staged", []byte("world"), 0o644); err != nil {
					return err
				}
				return os.Rename("staged", "renamed")
			},
			"model through the gateway": func() error {
				req, _ := http.NewRequest("POST", os.Getenv(modelEnv)+"/v1/messages", strings.NewReader("{}"))
				req.Header.Set("X-Api-Key", modelprovider.Placeholder)
				resp, err := (&http.Client{Timeout: wait}).Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if body, _ := io.ReadAll(resp.Body); resp.StatusCode != http.StatusOK || string(body) != "answer" {
					return fmt.Errorf("answered %d %q", resp.StatusCode, body)
				}
				return nil
			},
			"no direct route": func() error {
				c, err := net.DialTimeout("tcp", "192.0.2.1:80", 2*time.Second)
				if err == nil {
					c.Close()
					return errors.New("connected outside the gateway")
				}
				if !errors.Is(err, syscall.ENETUNREACH) {
					return fmt.Errorf("dial: %v, want ENETUNREACH", err)
				}
				return nil
			},
			"world is noexec": func() error {
				if err := exec.Command("/bin/true").Run(); !errors.Is(err, fs.ErrPermission) {
					return fmt.Errorf("exec of a world binary: %v, want a permission error", err)
				}
				return nil
			},
			"masks": func() error {
				for _, p := range []string{"/etc/ld.so.preload", "/etc/hostname"} {
					if data, err := os.ReadFile(p); err != nil || len(data) != 0 {
						return fmt.Errorf("%s holds %d bytes, %v", p, len(data), err)
					}
				}
				if entries, err := os.ReadDir("/etc/apt"); err != nil || len(entries) != 0 {
					return fmt.Errorf("/etc/apt holds %d entries, %v", len(entries), err)
				}
				return nil
			},
			"home": func() error {
				return os.WriteFile(agent.ViewPrivateRoot+"/"+agent.ViewHomeName+"/probe", []byte("x"), 0o600)
			},
			"passwd": func() error {
				data, err := os.ReadFile("/etc/passwd")
				if want := fmt.Sprintf("oac:x:%d:%d:oac:/.oac/home:/bin/bash\n", os.Getuid(), os.Getgid()); err != nil || !strings.Contains(string(data), want) {
					return fmt.Errorf("/etc/passwd lacks %q: %v", want, err)
				}
				return nil
			},
			"CA directory": func() error {
				data, err := os.ReadFile(filepath.Join(os.Getenv(caEnv), "ca.pem"))
				if block, _ := pem.Decode(data); err != nil || block == nil {
					return fmt.Errorf("no CA certificate: %v", err)
				}
				return nil
			},
		}
	case "touch":
		checks["touch"] = func() error { return os.WriteFile("touched", []byte("renewed"), 0o644) }
	case "home":
		checks["home kept"] = func() error {
			name := agent.ViewPrivateRoot + "/" + agent.ViewHomeName + "/probe"
			if data, err := os.ReadFile(name); err != nil || string(data) != "x" {
				return fmt.Errorf("%s holds %q, %v", name, data, err)
			}
			return nil
		}
	case "shim":
		// /bin/sh is the shim, so the command runs in the sandbox.
		cmd := exec.Command("/bin/sh", "-c", "echo $((6*7)); exit 3")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		var exit *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exit) {
			return exit.ExitCode()
		} else if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 125
		}
		return 0
	case "wait":
		// Beat in the world until the view ends.
		for i := 0; i < 600; i++ {
			os.WriteFile("beat", []byte(strconv.Itoa(i)), 0o644)
			os.ReadDir(".")
			time.Sleep(100 * time.Millisecond)
		}
	default:
		return 2
	}
	report := map[string]string{}
	for name, check := range checks {
		report[name] = ""
		if err := check(); err != nil {
			report[name] = err.Error()
		}
	}
	json.NewEncoder(os.Stdout).Encode(report)
	return 0
}
