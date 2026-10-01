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
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
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
// no AppArmor confinement and a static oac-sandbox-io, whose world is the
// container's /. Run it in a throwaway container:
//
//	CGO_ENABLED=0 go build -o /tmp/oac-sandbox-io ./apps/sandboxio/cmd/oac-sandbox-io
//	CGO_ENABLED=0 go test -c -o /tmp/agenthost.test ./apps/daemon/internal/agenthost
//	docker run --rm --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
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
	closure := t.TempDir()
	if err := os.Chmod(closure, 0o755); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, filepath.Join(closure, "harness"))
	register(reg, "test", &agent.View{
		Closure:   []agent.ViewMount{{Name: "harness", HostDir: closure}},
		Masks:     []agent.ViewMask{{Path: "/etc/ld.so.preload"}, {Path: "/etc/hostname"}, {Path: "/etc/apt", Dir: true}},
		LocalExec: []string{harnessPath},
		Proxy:     agent.ViewProxyNone,
		Executor: func(_ context.Context, req proto.PromptRequestPayload, s agent.ViewSession) (agent.Executor, error) {
			provider, err := modelprovider.ParseProvider(req.AgentOptions["model_provider"])
			if err != nil {
				return nil, err
			}
			return &testExecutor{session: s, dir: req.LocalEnvironment.WorkspaceRoot,
				env: []string{harnessEnv + "=1", modelEnv + "=" + provider.BaseURL, caEnv + "=" + cfg.CADir}}, nil
		},
	})
	sb.auth.AddRuntime(cfg.Credential, cfg.RuntimeID)
	sb.ready(t, cfg)
	workspace, err := os.MkdirTemp("/tmp", "agenthost-workspace-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
	if err := os.Chmod(workspace, 0o777); err != nil {
		t.Fatal(err)
	}

	t.Run("one Session", func(t *testing.T) {
		s := startSession(t, cfg, sb, request("test", workspace, upstream.URL, upstreamKey), 2*time.Second)
		r := s.turn(t, "check")
		for _, name := range harnessChecks {
			if msg, ok := r.Checks[name]; !ok || msg != "" {
				t.Errorf("%s: %q", name, msg)
			}
		}
		if r.Exit != "" {
			t.Errorf("Harness: %s; stderr %s", r.Exit, r.Stderr)
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
		if r := s.turn(t, "touch"); r.Checks["touch"] != "" || r.Exit != "" {
			t.Errorf("touch after the first lease: %+v", r)
		}
		if data, err := os.ReadFile(filepath.Join(workspace, "touched")); err != nil || string(data) != "renewed" {
			t.Errorf("the file written after the first lease holds %q, %v", data, err)
		}
		close(s.in)
		if err := s.wait(t); err != nil {
			t.Fatalf("Run = %v", err)
		}
		if err := sb.renew(t, cfg, s.binding); !errors.Is(err, sandboxlink.LeaseExpired) {
			t.Errorf("Renew after teardown = %v, want LeaseExpired for a closed attachment", err)
		}
		checkReleased(t, cfg)
	})

	t.Run("a restarted sandbox service fails the Session", func(t *testing.T) {
		s := startSession(t, cfg, sb, request("test", workspace, upstream.URL, upstreamKey), time.Minute)
		s.send(t, "wait")
		beat := filepath.Join(workspace, "beat")
		for deadline := time.Now().Add(wait); ; time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(beat); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the Harness never wrote to the world")
			}
		}
		sb.stop()
		sb.start(t)
		err := s.wait(t)
		t.Logf("Run = %v", err)
		if !errors.Is(err, ErrLink) && !errors.Is(err, ErrWorld) {
			t.Errorf("Run = %v, want ErrLink or ErrWorld", err)
		}
		if errors.Is(err, ErrTeardown) {
			t.Errorf("teardown incomplete: %v", err)
		}
		checkReleased(t, cfg)
	})

	t.Run("Sweep ends a lingering Session process", func(t *testing.T) {
		id := cfg.UIDs.First + 1
		cmd := exec.Command("/bin/sleep", "60")
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: id, Gid: id}}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		if err := Sweep(cfg); err != nil {
			t.Fatalf("Sweep = %v", err)
		}
		select {
		case <-done:
		case <-time.After(wait):
			cmd.Process.Kill()
			t.Fatal("the process with a Session uid still runs after Sweep")
		}
	})

	t.Run("Sweep ends a view whose leader thread has exited", func(t *testing.T) {
		id := cfg.UIDs.First + 2
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := inPIDNamespace(id, exe)
		cmd.Env = append(os.Environ(), zombieLeaderEnv+"=1")
		done := startView(t, cmd)
		until(t, "a zombie leader with a running thread", func() bool { return zombieLeaderHolds(id) })
		if got, err := allocUID(UIDRange{First: id, Count: 1}, procfs{}); !errors.Is(err, ErrCapacity) {
			freeUID(got)
			t.Errorf("allocUID beside a running thread = %d, %v", got, err)
		}
		sweepEnds(t, cfg, id, done)
	})

	t.Run("Sweep ends a view whose processes fork", func(t *testing.T) {
		id := cfg.UIDs.First + 3
		// Each subshell forks a sleep and exits at once.
		done := startView(t, inPIDNamespace(id, "/bin/sh", "-c", "while :; do (sleep 60 &); sleep 0.01; done"))
		until(t, "forked processes", func() bool { return processesHolding(id) >= 3 })
		sweepEnds(t, cfg, id, done)
	})
}

// inPIDNamespace returns a command that runs argv with uid under a root init
// in a new PID namespace, as a view does. The init starts argv again whenever
// it ends, so only ending the namespace ends it.
func inPIDNamespace(uid uint32, argv ...string) *exec.Cmd {
	script := `while :; do setpriv --reuid="$0" --regid="$0" --clear-groups -- "$@"; done`
	cmd := exec.Command("/bin/sh", append([]string{"-c", script, strconv.Itoa(int(uid))}, argv...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWPID}
	return cmd
}

// startView starts cmd and returns its end.
func startView(t *testing.T, cmd *exec.Cmd) <-chan error {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { cmd.Process.Kill() })
	return done
}

// sweepEnds checks that Sweep ends the view whose init done reports and every
// process that holds id.
func sweepEnds(t *testing.T, cfg Config, id uint32, done <-chan error) {
	t.Helper()
	if err := Sweep(cfg); err != nil {
		t.Fatalf("Sweep = %v", err)
	}
	select {
	case <-done:
	case <-time.After(wait):
		t.Fatal("the view's init still runs after Sweep")
	}
	if n := processesHolding(id); n != 0 {
		t.Errorf("%d processes hold uid %d after Sweep", n, id)
	}
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

// processesHolding counts the processes with a running thread whose real uid
// is id.
func processesHolding(id uint32) int {
	procs := map[int]bool{}
	for _, t := range threads() {
		if t.running && t.uids[0] == id {
			procs[t.tgid] = true
		}
	}
	return len(procs)
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
	rl, err := relay.New(relay.Config{Authority: auth})
	if err != nil {
		t.Fatal(err)
	}
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

func (sb *sandbox) grant(b Binding, runtimeID sandboxwire.ID, lease time.Duration) {
	sb.auth.AddGrant(b.AttachGrant, sandboxlinktest.Grant{RuntimeID: runtimeID, Resource: b.Resource, SessionID: b.SessionID,
		AssignmentID: b.AssignmentID, AssignmentEpoch: b.AssignmentEpoch, Lease: lease,
		Services: []sandboxlink.Service{sandboxlink.ServiceFile, sandboxlink.ServiceProcess, sandboxlink.ServiceNetwork}})
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
	probe, _, _ := newSession(sb.resource, proto.PromptRequestPayload{})
	b := probe.Binding
	sb.grant(b, cfg.RuntimeID, time.Minute)
	link := sb.dial(t, cfg)
	defer link.Close()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	for {
		st, _, err := link.OpenService(ctx, sandboxlink.Open{Service: sandboxlink.ServiceFile, Version: sandboxfs.Version, Resource: b.Resource,
			AttachmentID: b.AttachmentID, SessionID: b.SessionID, AssignmentID: b.AssignmentID, AssignmentEpoch: b.AssignmentEpoch, AttachGrant: b.AttachGrant})
		if err == nil {
			st.Close()
			break
		}
		if !errors.Is(err, sandboxlink.ServiceUnavailable) || ctx.Err() != nil {
			t.Fatalf("oac-sandbox-io is not serving: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := link.CloseAttachment(ctx, b.AttachmentID); err != nil {
		t.Fatal(err)
	}
}

// renew renews b's attachment from a fresh link.
func (sb *sandbox) renew(t *testing.T, cfg Config, b Binding) error {
	link := sb.dial(t, cfg)
	defer link.Close()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	_, err := link.Renew(ctx, sandboxlink.RenewAttachment{AttachmentID: b.AttachmentID, AttachGrant: b.AttachGrant})
	return err
}

// sessionRun is a running Session under test.
type sessionRun struct {
	binding Binding
	in      chan Input
	out     chan proto.Envelope
	done    chan error
}

func startSession(t *testing.T, cfg Config, sb *sandbox, req proto.PromptRequestPayload, lease time.Duration) *sessionRun {
	s, in, out := newSession(sb.resource, req)
	sb.grant(s.Binding, cfg.RuntimeID, lease)
	r := &sessionRun{binding: s.Binding, in: in, out: out, done: make(chan error, 1)}
	go func() {
		r.done <- run(context.Background(), cfg, s, deps{dial: relayDial(cfg), broker: func() processBroker { return noBroker{} }, procs: procfs{}})
	}()
	return r
}

func (r *sessionRun) send(t *testing.T, mode string) {
	t.Helper()
	select {
	case r.in <- Input{RunID: mode, Message: proto.TextInput(mode)}:
	case err := <-r.done:
		t.Fatalf("Run ended before the %s Turn: %v", mode, err)
	case <-time.After(wait):
		t.Fatalf("Run took no %s Turn", mode)
	}
}

func (r *sessionRun) turn(t *testing.T, mode string) report {
	t.Helper()
	r.send(t, mode)
	select {
	case e := <-r.out:
		var rep report
		if err := json.Unmarshal(e.Payload, &rep); err != nil {
			t.Fatal(err)
		}
		return rep
	case err := <-r.done:
		t.Fatalf("Run ended during the %s Turn: %v", mode, err)
	case <-time.After(wait):
		t.Fatalf("no %s report", mode)
	}
	return report{}
}

func (r *sessionRun) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		return err
	case <-time.After(3 * wait):
		t.Fatal("Run did not return")
		return nil
	}
}

// checkReleased checks that no Session directory, mount or process remains.
func checkReleased(t *testing.T, cfg Config) {
	t.Helper()
	if left := leftSessions(t, cfg); len(left) != 0 {
		t.Errorf("%d Session directories remain", len(left))
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mounts), cfg.StateDir) {
		t.Error("a mount under the state directory remains")
	}
	procs, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range procs {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		status, err := os.ReadFile(filepath.Join("/proc", p.Name(), "status"))
		if err != nil {
			continue
		}
		for line := range strings.Lines(string(status)) {
			if fields := strings.Fields(line); len(fields) > 1 && fields[0] == "Uid:" {
				if uid, _ := strconv.ParseUint(fields[1], 10, 32); uid >= uint64(cfg.UIDs.First) && uid < uint64(cfg.UIDs.First+cfg.UIDs.Count) {
					t.Errorf("process %s runs with Session uid %d", p.Name(), uid)
				}
			}
		}
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

// report is a Turn's one envelope: the Harness's checks, its stderr and how
// it exited.
type report struct {
	Checks map[string]string `json:"checks"`
	Stderr string            `json:"stderr"`
	Exit   string            `json:"exit"`
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
	json.Unmarshal(stdout, &r.Checks)
	if err := t.p.Wait(); err != nil {
		r.Exit = err.Error()
	}
	r.Stderr = stderr.String()
	payload, _ := json.Marshal(r)
	out <- proto.Envelope{Type: proto.TypeOutputMessage, ID: runID, Payload: payload}
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
