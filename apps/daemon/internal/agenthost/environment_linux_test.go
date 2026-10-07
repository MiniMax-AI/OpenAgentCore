//go:build linux

package agenthost

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// TestEnvironmentOwnerServesTheSandbox prepares an Environment with a Skill
// and a setup step through runtime_prepare, refuses a plugin whose MCP server
// it cannot serve, reopens the Environment on a new Router without
// initializing it again, quiesces and resumes it, and checks that a File
// mutation whose outcome is unknown is never replayed. It runs with the view
// suite; see the comment there.
func TestEnvironmentOwnerServesTheSandbox(t *testing.T) {
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a throwaway container; see the view suite", gateEnv)
	}
	sb := startSandbox(t, os.Getenv(sandboxIOEnv))
	// The sandbox's world is this container's /.
	for _, p := range []string{sandboxInitialization, path.Join(sandboxWorkspace, "setup.txt"), path.Join(sandboxWorkspace, "notes")} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(sandboxWorkspace, 0o777); err != nil {
		t.Fatal(err)
	}
	harnesses := agent.NewRegistry()
	register(harnesses, "test", &agent.View{Proxy: agent.ViewProxyEnv, Executor: func(context.Context, proto.PromptRequestPayload, agent.ViewSession) (agent.Executor, error) {
		return nil, errors.New("the test's factory replaces the view's")
	}})
	cfg := Config{StateDir: t.TempDir(), RelayURL: sb.url, RuntimeID: sandboxwire.NewID(), Credential: []byte("runtime-credential"), Harnesses: harnesses}
	sb.auth.AddRuntime(cfg.Credential, cfg.RuntimeID)
	sb.ready(t, cfg)
	p := &probe{}
	h := &Host{cfg: cfg, owners: owners{d: deps{dial: relayDial(cfg), stream: p.stream}}}
	// The factory records what the owner prepared: admission does not run
	// Skills in views yet.
	prepared := make(chan preparedExecutor, 4)
	reg := registry(harnesses, func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
		_, env, err := h.executor(ctx, req)
		prepared <- preparedExecutor{req: req, env: env}
		if err != nil {
			return nil, err
		}
		return &fakeExecutor{close: func() error { return nil }}, nil
	})
	b := sb.bind(cfg.RuntimeID, time.Minute)
	skill := agentskill.Metadata{Type: "inline", Name: "probe-skill", Description: "Probe the installation."}
	manifest := []byte("---\nname: probe-skill\ndescription: Probe the installation.\n---\nProbe.\n")
	req := request("test", sandboxWorkspace, "https://model.invalid", "key")
	req.LocalEnvironment.CapabilitySources = &agentcapabilities.Input{Skills: []agentskill.Metadata{skill}}
	req.LocalEnvironment.Capabilities = true

	// Prepare as Core does: configure, a setup step that sees the tool
	// environment, the Skill and finalize, then the Executor. A plugin whose
	// stdio MCP server takes credentials from the Environment fails first,
	// before anything of it is staged.
	first := &daemon{host: h}
	first.route(t, reg)
	first.assign(t, b)
	plugin := agentplugin.Metadata{Type: "inline", Name: "package", Description: "Package proof."}
	configured := archive(t, "package", map[string][]byte{".codex-plugin/plugin.json": []byte(`{"name":"package","description":"Package proof."}`),
		".mcp.json": []byte(`{"mcpServers":{"local":{"command":"python3","env_vars":["PROBE"]}}}`)})
	if r := first.runtimePrepare(t, b, proto.RuntimePreparePayload{Action: "plugin", Plugin: &plugin}, configured); r.Outcome != "failed" {
		t.Fatalf("runtime_prepare of a plugin whose stdio server takes Environment credentials: %+v, want failed", r)
	}
	if _, err := os.Lstat(path.Join(agentcapabilities.Directory, "plugins")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the refused plugin was staged: %v", err)
	}
	for _, step := range []struct {
		begin proto.RuntimePreparePayload
		data  []byte
	}{
		{proto.RuntimePreparePayload{Action: "initialize", Initialization: &proto.RuntimeInitialization{Action: "configure", Env: map[string]string{"PROBE": "probe-value"}}}, nil},
		{proto.RuntimePreparePayload{Action: "initialize", Initialization: &proto.RuntimeInitialization{Action: "setup", Command: `printf '%s\n' "$PROBE" >> setup.txt`}}, nil},
		{proto.RuntimePreparePayload{Action: "skill", Skill: &skill}, archive(t, "probe-skill", map[string][]byte{"SKILL.md": manifest})},
		{proto.RuntimePreparePayload{Action: "finalize", Sources: req.LocalEnvironment.CapabilitySources}, nil},
	} {
		if r := first.runtimePrepare(t, b, step.begin, step.data); r.Outcome != "completed" {
			t.Fatalf("runtime_prepare %s: %+v, want completed", step.begin.Action, r)
		}
	}
	if _, status := first.prepare(t, b, req); status.State != "ready" {
		t.Fatalf("the preparation is %s (%s), want ready", status.State, status.ErrorCode)
	}
	got := <-prepared
	local := got.req.LocalEnvironment
	if local.WorkspaceRoot != sandboxWorkspace || local.CapabilityRoot != agentcapabilities.Directory || len(local.Skills) != 1 ||
		local.Skills[0].Metadata != skill || local.Skills[0].InstallationRoot != agentcapabilities.Directory || local.Skills[0].RelativeRoot != "skills/probe-skill" {
		t.Fatalf("the Executor's Environment is %+v", local)
	}
	if got.env.Tool["PROBE"] != "probe-value" || got.env.Sandbox["PATH"] != sandboxBaseline["PATH"] {
		t.Fatalf("the Executor's environments are %+v", got.env)
	}
	if body, err := os.ReadFile(path.Join(agentcapabilities.Directory, "skills/probe-skill/SKILL.md")); err != nil || !bytes.Equal(body, manifest) {
		t.Fatalf("the installed Skill is %q, %v", body, err)
	}
	checkSetup(t)
	if err := first.shutdown(); err != nil {
		t.Fatal(err)
	}

	// Reopen: a new Router checks the completed installation and neither
	// changes the world nor runs a step.
	p.reset(0)
	second := &daemon{host: h}
	second.route(t, reg)
	id, status := second.prepare(t, b, req)
	if status.State != "ready" {
		t.Fatalf("the reopened preparation is %s (%s), want ready", status.State, status.ErrorCode)
	}
	if got := <-prepared; len(got.req.LocalEnvironment.Skills) != 1 || got.env.Tool["PROBE"] != "probe-value" {
		t.Fatalf("the reopened Executor's Environment is %+v, %+v", got.req.LocalEnvironment, got.env)
	}
	if n := p.counts(); n.mutations != 0 || n.processes != 0 {
		t.Fatalf("the reopen sent %d File mutations and opened %d Process streams", n.mutations, n.processes)
	}
	checkSetup(t)

	// Quiesce drains the owner; after Resume it serves a read on a new
	// attachment.
	second.release(t, b, id, status.Handle)
	suspension := proto.EnvironmentSuspendPayload{EnvironmentID: environmentID(b), SuspendID: "suspend-" + uuid.NewString()}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	if err := second.router.Quiesce(ctx, ref(b), suspension); err != nil {
		t.Fatalf("Quiesce: %v", err)
	}
	if !h.drained(t, b) {
		t.Fatal("the quiesced owner kept its attachment")
	}
	if err := second.router.Resume(ref(b), suspension, second); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	p.reset(0)
	id, status = second.prepare(t, b, req)
	if status.State != "ready" {
		t.Fatalf("the resumed preparation is %s (%s), want ready", status.State, status.ErrorCode)
	}
	if r := second.read(t, b, status.Handle); r.Outcome != "completed" || !lists(r.Directory, "setup.txt") {
		t.Fatalf("the resumed read is %+v", r)
	}
	if p.counts().files != 1 {
		t.Fatalf("the resumed read opened %d File streams, want 1", p.counts().files)
	}
	second.release(t, b, id, status.Handle)

	// An uncertain File mutation quarantines the owner: no later write or
	// runtime_prepare on any Router sends one again.
	p.reset(sandboxfs.OpLink)
	if r := second.write(t, b, "notes/uncertain.txt", []byte("uncertain")); r.Outcome != "unknown" {
		t.Fatalf("the interrupted write is %+v, want unknown", r)
	}
	// The Router reports the uncertain write as it shuts down and still
	// drains the owner.
	second.shutdown()
	if !h.drained(t, b) {
		t.Fatal("the owner kept its attachment after the Router shut down")
	}
	p.reset(0)
	third := &daemon{host: h}
	third.route(t, reg)
	third.assign(t, b)
	if r := third.write(t, b, "notes/uncertain.txt", []byte("uncertain")); r.Outcome != "unknown" {
		t.Fatalf("the write after an uncertain one is %+v, want unknown", r)
	}
	// An unknown outcome fences the Router's transfers, so the next one runs
	// on another.
	third.shutdown()
	fourth := &daemon{host: h}
	fourth.route(t, reg)
	fourth.assign(t, b)
	if r := fourth.runtimePrepare(t, b, proto.RuntimePreparePayload{Action: "file", File: &proto.RuntimeInitialFile{Path: "/workspace/notes/file.txt"}}, []byte("file")); r.Outcome != "unknown" {
		t.Fatalf("the runtime_prepare after an uncertain write is %+v, want unknown", r)
	}
	if n := p.counts(); n.files != 0 || n.mutations != 0 {
		t.Fatalf("the quarantined owner opened %d File streams and sent %d mutations", n.files, n.mutations)
	}
	for _, name := range []string{"uncertain.txt", "file.txt"} {
		if _, err := os.Lstat(path.Join(sandboxWorkspace, "notes", name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s exists: %v", name, err)
		}
	}
}

// TestUnreachableSandboxRejectsRuntimePreparation checks that a
// runtime_prepare whose owner cannot reach the sandbox ends rejected, which
// leaves the Router free to run another Session's and to shut down.
func TestUnreachableSandboxRejectsRuntimePreparation(t *testing.T) {
	var dials atomic.Int32
	dm := newDaemon(t, newViewFixture(t).cfg, deps{dial: countingDial(&dials), tasks: noTasks})
	configure := proto.RuntimePreparePayload{Action: "initialize", Initialization: &proto.RuntimeInitialization{Action: "configure"}}
	for _, b := range []Binding{newBinding(newResource()), newBinding(newResource())} {
		dm.assign(t, b)
		if r := dm.runtimePrepare(t, b, configure, nil); r.Outcome != "rejected" || r.ErrorCode != "resource_unavailable" {
			t.Fatalf("runtime_prepare is %+v, want rejected with resource_unavailable", r)
		}
	}
	if err := dm.shutdown(); err != nil || dials.Load() != 2 {
		t.Fatalf("Shutdown after %d dials: %v", dials.Load(), err)
	}
}

type preparedExecutor struct {
	req proto.PromptRequestPayload
	env Environment
}

// checkSetup checks that the setup step ran once.
func checkSetup(t *testing.T) {
	t.Helper()
	if body, err := os.ReadFile(path.Join(sandboxWorkspace, "setup.txt")); err != nil || string(body) != "probe-value\n" {
		t.Fatalf("the setup step wrote %q, %v", body, err)
	}
}

// archive zips files below the archive root root.
func archive(t *testing.T, root string, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		f, err := w.Create(root + "/" + name)
		if err == nil {
			_, err = f.Write(files[name])
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// drained reports whether b's Session's Environment owner holds no
// attachment.
func (h *Host) drained(t *testing.T, b Binding) bool {
	t.Helper()
	h.owners.mu.Lock()
	o := h.owners.m[b.SessionID]
	h.owners.mu.Unlock()
	if o == nil {
		t.Fatal("the Session has no Environment owner")
	}
	if err := o.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer o.release()
	return o.link == nil
}

// runtimePrepare sends one runtime_prepare transfer of data on b's Session
// and returns its result.
func (dm *daemon) runtimePrepare(t *testing.T, b Binding, begin proto.RuntimePreparePayload, data []byte) proto.RuntimePrepareResultPayload {
	t.Helper()
	begin.Step, begin.EnvironmentID, begin.SessionID = "begin", environmentID(b), ref(b).SessionID
	if begin.Action != "initialize" && begin.Action != "finalize" {
		digest := sha256.Sum256(data)
		begin.SizeBytes, begin.SHA256 = len(data), hex.EncodeToString(digest[:])
	}
	id := uuid.NewString()
	var r proto.RuntimePrepareResultPayload
	send := func(payload proto.RuntimePreparePayload) {
		dm.handle(t, ref(b), proto.TypeRuntimePrepare, id, payload)
		if err := dm.next(t, id).DecodePayload(&r); err != nil {
			t.Fatal(err)
		}
	}
	if send(begin); r.Outcome != "ready" {
		return r
	}
	for off := 0; off < len(data); off += proto.RuntimePrepareChunkBytes {
		if send(proto.RuntimePreparePayload{Step: "chunk", Offset: off, Data: data[off:min(len(data), off+proto.RuntimePrepareChunkBytes)]}); r.Outcome != "received" {
			t.Fatalf("runtime_prepare chunk: %+v", r)
		}
	}
	send(proto.RuntimePreparePayload{Step: "commit"})
	return r
}

// write writes data to p in b's Session's workspace and returns the result.
func (dm *daemon) write(t *testing.T, b Binding, p string, data []byte) proto.WorkspaceWriteResultPayload {
	t.Helper()
	digest := sha256.Sum256(data)
	id := uuid.NewString()
	var r proto.WorkspaceWriteResultPayload
	send := func(payload proto.WorkspaceWritePayload) {
		dm.handle(t, ref(b), proto.TypeWorkspaceWrite, id, payload)
		if err := dm.next(t, id).DecodePayload(&r); err != nil {
			t.Fatal(err)
		}
	}
	if send(proto.WorkspaceWritePayload{Step: "begin", EnvironmentID: environmentID(b), SessionID: ref(b).SessionID, Path: p, SizeBytes: len(data),
		SHA256: hex.EncodeToString(digest[:])}); r.Outcome != "ready" {
		return r
	}
	if send(proto.WorkspaceWritePayload{Step: "chunk", Data: data}); r.Outcome != "received" {
		t.Fatalf("workspace_write chunk: %+v", r)
	}
	send(proto.WorkspaceWritePayload{Step: "commit"})
	return r
}

// read lists the workspace of b's Session under the ready preparation handle.
func (dm *daemon) read(t *testing.T, b Binding, handle string) proto.WorkspaceReadResultPayload {
	t.Helper()
	id := uuid.NewString()
	dm.handle(t, ref(b), proto.TypeWorkspaceRead, id, proto.WorkspaceReadPayload{Handle: handle, EnvironmentID: environmentID(b), MaxEntries: 100})
	var r proto.WorkspaceReadResultPayload
	if err := dm.next(t, id).DecodePayload(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

// release releases the preparation that request id made.
func (dm *daemon) release(t *testing.T, b Binding, id, handle string) {
	t.Helper()
	dm.handle(t, ref(b), proto.TypeExecutionRelease, id, proto.ExecutionReleasePayload{Handle: handle})
	var status proto.PreparationStatusPayload
	if err := dm.next(t, id).DecodePayload(&status); err != nil || status.State != "released" {
		t.Fatalf("the release is %+v, %v", status, err)
	}
}

func lists(d *proto.WorkspaceDirectoryResult, name string) bool {
	for _, e := range d.Entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

// probe observes the streams an Environment owner opens: it counts File
// streams, File mutations and Process streams, and breaks the File stream
// that sends the next request of an armed operation before sending it.
type probe struct {
	mu    sync.Mutex
	n     probeCounts
	armed sandboxfs.Op
}

type probeCounts struct{ files, mutations, processes int }

var mutations = map[sandboxfs.Op]bool{sandboxfs.OpSetAttr: true, sandboxfs.OpCreate: true, sandboxfs.OpWrite: true, sandboxfs.OpFsync: true,
	sandboxfs.OpMkdir: true, sandboxfs.OpUnlink: true, sandboxfs.OpRmdir: true, sandboxfs.OpRename: true, sandboxfs.OpLink: true, sandboxfs.OpSymlink: true}

// reset clears the counts and arms op; zero arms none.
func (p *probe) reset(op sandboxfs.Op) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n, p.armed = probeCounts{}, op
}

func (p *probe) counts() probeCounts {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

func (p *probe) stream(service sandboxlink.Service, st io.ReadWriteCloser) io.ReadWriteCloser {
	p.mu.Lock()
	defer p.mu.Unlock()
	if service == sandboxlink.ServiceProcess {
		p.n.processes++
		return st
	}
	p.n.files++
	return &probed{ReadWriteCloser: st, p: p}
}

type probed struct {
	io.ReadWriteCloser
	p *probe
}

// Write sees one whole frame per call, as sandboxwire.WriteFrame writes it.
func (s *probed) Write(b []byte) (int, error) {
	f, err := sandboxwire.ReadFrame(bytes.NewReader(b), sandboxwire.MaxPayload)
	if err != nil {
		return 0, err
	}
	op := sandboxfs.Op(f.Type)
	s.p.mu.Lock()
	if mutations[op] {
		s.p.n.mutations++
	}
	broken := op == s.p.armed
	if broken {
		s.p.armed = 0
	}
	s.p.mu.Unlock()
	if broken {
		s.Close()
		return 0, errors.New("the probe broke the stream")
	}
	return s.ReadWriteCloser.Write(b)
}
