//go:build linux

// Package agenthostqualify runs one agent-host Session per real Harness
// through the daemon's dispatch against a sandbox container running
// oac-sandbox-io, with a real model behind the gateway.
package agenthostqualify

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/claudesdk"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/codex"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/mcode"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agenthost"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview/sessionviewtest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// scripts/qualify-agent-host.sh runs this test binary as the agent host in
// the agent-host image, which activates each Harness from the image's
// manifest, and oac-sandbox-io in the sandbox image, both on the host network.
// The test serves the Link test relay over WSS on loopback and hands the
// sandbox its bootstrap and the relay's CA through a shared run directory.
// Each OAC_QUALIFY_<KIND> holds the Harness's model and model_provider without
// api_key, which the test reads from OAC_QUALIFY_KEY_FILE.
const (
	gateEnv = "OAC_TEST_QUALIFY"
	keyEnv  = "OAC_QUALIFY_KEY_FILE"
	runDir  = "/run/qualify"
	shim    = "/opt/oac/bin/oac-process-shim"
	// caDir holds the agent-host image's roots, one regular PEM file each.
	caDir = "/usr/share/ca-certificates/mozilla"
	// workspace is the sandbox directory every Session works in.
	workspace = "/workspace/custom-project"
	turnLimit = 10 * time.Minute
)

var harnesses = []agent.Declaration{claudesdk.Declaration, codex.Declaration, mcode.Declaration}

func TestMain(m *testing.M) {
	sessionview.Init()
	os.Exit(m.Run())
}

func TestHarnessSessionsAgainstTheSandbox(t *testing.T) {
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test with scripts/qualify-agent-host.sh", gateEnv)
	}
	if err := sessionview.Probe(); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	key, err := os.ReadFile(os.Getenv(keyEnv))
	if err != nil {
		t.Fatalf("model key: %v", err)
	}
	activate(t)
	tunnel(t)
	sb := startSandbox(t)
	reg := agent.NewRegistry()
	cfg := agenthost.Config{StateDir: t.TempDir(), ViewCgroups: sessionviewtest.CgroupParent(t), UIDs: agenthost.UIDRange{First: 70000, Count: 8}, RelayURL: sb.url, TLS: sb.tls,
		RuntimeID: sandboxwire.NewID(), Credential: []byte("runtime-credential"), Harnesses: reg, Shim: shim, CADir: caDir,
		Log: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	sb.auth.AddRuntime(cfg.Credential, cfg.RuntimeID)
	sb.ready(t, cfg)
	// The logical workspace remains distinct from the bound physical root.
	sb.files(t, cfg, func(ctx context.Context, c *sandboxfs.Client, root sandboxfs.NodeRef) {
		walked, err := c.Walk(ctx, &sandboxfs.WalkRequest{Parent: root, Names: [][]byte{[]byte("workspace")}})
		if err != nil || walked.Failure != nil {
			t.Fatalf("workspace parent: %v %+v", err, walked)
		}
		if _, err := c.Mkdir(ctx, &sandboxfs.MkdirRequest{Parent: walked.Entries[0].Node, Name: []byte("custom-project"), Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	})
	h, err := agenthost.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer h.Close()

	for _, declaration := range harnesses {
		kind := declaration.Info.Kind
		t.Run(kind, func(t *testing.T) {
			raw := os.Getenv("OAC_QUALIFY_" + strings.ToUpper(kind))
			if raw == "" {
				t.Skipf("set OAC_QUALIFY_%s to the Harness's model and model_provider", strings.ToUpper(kind))
			}
			runtime := declaration.Discover(context.Background(), agent.DiscoveryOptions{Stdout: io.Discard, Stderr: os.Stderr}, declaration.Info)
			if runtime == nil || runtime.View == nil {
				t.Fatalf("%s declares no agent-host view; discovery reported why above", kind)
			}
			reg.RegisterKind(runtime.Info, declaration.Configuration)
			reg.RegisterView(kind, *runtime.View)
			qualify(t, h, cfg, sb, kind, runtime.Info.Capabilities, sessionModel(t, raw, key))
		})
	}
}

// qualify runs the kind's Turns through dispatch. runtime_prepare first
// freezes the Session's tool environment and runs a setup step that writes a
// file with a value only the tool environment holds. The first Turn writes a
// file, runs a failing command and reports what it printed and its exit
// status, then the test checks all of them. A view that declares function
// tools runs a second Turn in a new Executor, which resumes the Session's
// native history, and calls a function there. A view that declares tool
// search runs a Turn in a new Executor without native history that finds the
// function, deferred, with tool search. A view that declares environment none
// answers a Turn in a Session without an Environment, and its native state
// names the work directory. In a Session whose Environment installs a
// plugin, every view uses the plugin's Skill in one Turn and calls a tool of
// its stdio MCP server, which runs in the sandbox, in another. Each Executor
// after the first reopens the Environment that runtime_prepare prepared.
func qualify(t *testing.T, h *agenthost.Host, cfg agenthost.Config, sb *sandbox, kind string, caps proto.AgentKindCapabilities, model proto.PromptRequestPayload) {
	name := "qualify-" + kind + ".txt"
	value, content := strings.ToLower(rand.Text()), "qualified "+strings.ToLower(rand.Text()[:12])
	code, _ := rand.Int(rand.Reader, big.NewInt(90))
	exit := code.Int64() + 3
	prompt := "Use your tools for each step.\n" +
		"QUALIFY_VALUE and QUALIFY_EXIT are already set in the sandbox environment. Do not assign or modify either variable.\n" +
		"1. Create the file " + name + " in the current directory with exactly this content: " + content + "\n" +
		"2. Run this shell command exactly once: echo \"$QUALIFY_VALUE\"; exit \"$QUALIFY_EXIT\"\n" +
		"   It prints a value and exits with a non-zero status; that is expected.\n" +
		"3. Answer with exactly one line: VALUE=<the printed value> EXIT=<the exit status>"

	configuration := proto.PromptRequestPayload{AgentKind: kind, DisableSubagents: true,
		Model: model.Model, ModelProvider: model.ModelProvider, ExecutionControls: &proto.ExecutionControls{TextVerbosity: "medium"},
		LocalEnvironment: &proto.LocalEnvironment{ID: uuid.UUID(sb.resource.EnvironmentID).String(),
			CapabilitySources: &agentcapabilities.Input{}}}
	if caps.FunctionTools.IsSupported() {
		configuration.FunctionTools = []proto.FunctionTool{lookupTicket}
	}
	k := newTicket(t)
	// The sandbox holds one Environment Session's preparation at a time.
	sb.reset(t, cfg)
	s := sb.session(h, cfg, configuration)
	setup := "setup-" + kind + ".txt"
	s.prepare(t, nil,
		proto.RuntimeInitialization{Action: "configure", Env: map[string]string{"QUALIFY_VALUE": value, "QUALIFY_EXIT": fmt.Sprint(exit)}},
		proto.RuntimeInitialization{Action: "setup", Command: `printf '%s' "$QUALIFY_VALUE" > ` + setup})
	if got := sb.read(t, cfg, workspace+"/"+setup); got != value {
		t.Errorf("the setup step wrote %q, want the tool environment's %q", got, value)
	}
	done, answer, _ := s.turn(t, "qualify", prompt, k)
	if !strings.Contains(answer, "VALUE="+value) || !strings.Contains(answer, fmt.Sprintf("EXIT=%d", exit)) {
		t.Errorf("the answer %q does not report VALUE=%s EXIT=%d", answer, value, exit)
	}
	if got := sb.read(t, cfg, workspace+"/"+name); strings.TrimRight(got, "\n") != content {
		t.Errorf("%s holds %q, want %q", name, got, content)
	}

	if caps.FunctionTools.IsSupported() {
		// The first Executor retired with its Router.
		native, _ := done.Metadata[proto.DoneMetaAgentSessionID].(string)
		if native == "" {
			t.Fatal("the first Turn reported no native session to resume")
		}
		resumed := s.with(configuration)
		resumed.configuration.AgentSessionID = native
		done, answer, calls := resumed.turn(t, "resumed-function", k.prompt("Call the lookup_ticket function"), k)
		if resumed, _ := done.Metadata[proto.DoneMetaAgentSessionID].(string); resumed != native {
			t.Errorf("the resumed Turn reported the native session %q, want %q", resumed, native)
		}
		k.check(t, answer, calls)
	}
	if caps.ToolSearch.IsSupported() {
		deferred := lookupTicket
		deferred.DeferLoading = true
		configuration.ToolSearch, configuration.FunctionTools = true, []proto.FunctionTool{deferred}
		_, answer, calls := s.with(configuration).turn(t, "tool-search", k.prompt("Search your tools for the function that looks up support tickets"), k)
		k.check(t, answer, calls)
	}
	if caps.EnvironmentNone.IsSupported() {
		none := configuration
		none.LocalEnvironment, none.DisableExecutionEnvironment, none.FunctionTools, none.ToolSearch = nil, true, nil, false
		s := sb.session(h, cfg, none)
		_, answer, _ := s.turn(t, "environment-none", "What is 17 times 23? Answer with exactly one line: PRODUCT=<the number>", k)
		if !strings.Contains(answer, "PRODUCT=391") {
			t.Errorf("the answer %q does not report PRODUCT=391", answer)
		}
		s.checkCwd(t, agent.ViewPrivateRoot+"/"+agent.ViewHomeName+"/"+agent.ViewWorkName)
	}
	{
		// Claude does not combine Skills with tool search.
		word, code := strings.ToLower(rand.Text()[:12]), strings.ToLower(rand.Text()[:12])
		installed, local := configuration, *configuration.LocalEnvironment
		installed.FunctionTools, installed.ToolSearch = nil, false
		local.CapabilitySources = &agentcapabilities.Input{Plugins: []agentplugin.Metadata{qualifyPlugin}}
		installed.LocalEnvironment = &local
		sb.reset(t, cfg)
		s := sb.session(h, cfg, installed)
		s.prepare(t, pluginArchive(t, word, code))
		_, answer, _ := s.turn(t, "skill", "Use the qualify-word skill: read its SKILL.md, and call no MCP tool.\nAnswer with exactly one line: WORD=<the qualification word it tells>", k)
		if !strings.Contains(answer, "WORD="+word) {
			t.Errorf("the answer %q does not report WORD=%s", answer, word)
		}
		_, answer, _ = s.turn(t, "stdio-mcp", "Call the reveal_code tool of the qualify MCP server once.\nAnswer with exactly one line: CODE=<the code it returns>", k)
		if !strings.Contains(answer, "CODE="+code) {
			t.Errorf("the answer %q does not report CODE=%s", answer, code)
		}
	}
}

var qualifyPlugin = agentplugin.Metadata{Type: "inline", Name: "qualify", Description: "Qualification plugin."}

// pluginArchive is qualifyPlugin's archive. Its Skill tells word, and its
// stdio MCP server runs mcpServer from the package with code.
func pluginArchive(t *testing.T, word, code string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, body := range map[string]string{
		".codex-plugin/plugin.json":    `{"name":"qualify","description":"Qualification plugin.","skills":"./skills/"}`,
		".mcp.json":                    `{"mcpServers":{"qualify":{"command":"python3","args":["server.py","` + code + `"]}}}`,
		"server.py":                    mcpServer,
		"skills/qualify-word/SKILL.md": "---\nname: qualify-word\ndescription: Tells the qualification word.\n---\nThe qualification word is " + word + ".\n",
	} {
		f, err := w.Create("qualify/" + name)
		if err == nil {
			_, err = f.Write([]byte(body))
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

// mcpServer is a stdio MCP server whose one tool returns the code in its
// argument.
const mcpServer = `import json, sys
code = sys.argv[1]
for line in iter(sys.stdin.readline, ""):
    msg = json.loads(line) if line.strip() else {}
    if "id" not in msg or "method" not in msg:
        continue
    method, reply = msg["method"], {"jsonrpc": "2.0", "id": msg["id"]}
    if method == "initialize":
        reply["result"] = {"protocolVersion": msg["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "qualify", "version": "1"}}
    elif method == "tools/list":
        reply["result"] = {"tools": [{"name": "reveal_code", "description": "Returns the MCP server's code.", "inputSchema": {"type": "object", "properties": {}}}]}
    elif method == "tools/call":
        reply["result"] = {"content": [{"type": "text", "text": "The code is " + code + "."}]}
    elif method == "ping":
        reply["result"] = {}
    else:
        reply["error"] = {"code": -32601, "message": "method not found"}
    print(json.dumps(reply), flush=True)
`

// lookupTicket is the function the function Turns call.
var lookupTicket = proto.FunctionTool{Name: "lookup_ticket", Description: "Looks up a support ticket by its number.",
	Parameters: json.RawMessage(`{"type":"object","properties":{"ticket":{"type":"string","description":"The ticket number"}},"required":["ticket"],"additionalProperties":false}`)}

// ticket is lookup_ticket's result for number: a text with first, an image,
// and a text with second. An answer that holds both codes shows that the
// Harness gave its model the whole result.
type ticket struct{ number, first, second, image string }

func newTicket(t *testing.T) ticket {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	number, _ := rand.Int(rand.Reader, big.NewInt(9000))
	return ticket{number: fmt.Sprint(number.Int64() + 1000), first: strings.ToLower(rand.Text()[:8]), second: strings.ToLower(rand.Text()[:8]),
		image: "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())}
}

func (k ticket) result() []proto.InputContent {
	first, second := "Ticket "+k.number+": the first code is "+k.first+".", "The second code is "+k.second+"."
	return []proto.InputContent{{Type: "input_text", Text: &first}, {Type: "input_image", ImageURL: &k.image}, {Type: "input_text", Text: &second}}
}

// prompt asks for one call of the function that find finds.
func (k ticket) prompt(find string) string {
	return find + ", and call it once with ticket \"" + k.number + "\".\nAnswer with exactly one line: FIRST=<the first code> SECOND=<the second code>"
}

// check checks that the Turn called lookup_ticket for the ticket and
// answered with both codes.
func (k ticket) check(t *testing.T, answer string, calls []proto.FunctionCallPayload) {
	t.Helper()
	if len(calls) == 0 || calls[0].Name != lookupTicket.Name || !strings.Contains(string(calls[0].Arguments), k.number) {
		t.Errorf("the Turn made %d function calls, want the first to call %s for ticket %s", len(calls), lookupTicket.Name, k.number)
	}
	if !strings.Contains(answer, "FIRST="+k.first) || !strings.Contains(answer, "SECOND="+k.second) {
		t.Errorf("the answer %q does not report FIRST=%s SECOND=%s", answer, k.first, k.second)
	}
}

// session is a Session bound to the sandbox, in the sandbox's Environment
// unless its configuration disables it. Each Turn runs in a new Executor
// through a new dispatch Router, which binds the Session to the Host's
// Environment owner.
type session struct {
	h             *agenthost.Host
	cfg           agenthost.Config
	binding       agenthost.Binding
	id            string
	configuration proto.PromptRequestPayload
}

func (sb *sandbox) session(h *agenthost.Host, cfg agenthost.Config, configuration proto.PromptRequestPayload) *session {
	s := &session{h: h, cfg: cfg, binding: sb.binding(), configuration: configuration}
	s.id = uuid.UUID(s.binding.SessionID).String()
	sb.grant(s.binding, cfg.RuntimeID)
	return s
}

// with is the Session with configuration for its next Executors.
func (s *session) with(configuration proto.PromptRequestPayload) *session {
	next := *s
	next.configuration = configuration
	return &next
}

// router returns a new Router that serves the Host's kinds, with the
// Session bound to it.
func (s *session) router(t *testing.T, out sender, id string) (*dispatch.Router, proto.AssignmentRef) {
	t.Helper()
	router, err := dispatch.New(dispatch.Config{Sender: out, Environments: s.h.Environments, Log: s.cfg.Log, Registry: s.h.Registry()})
	if err != nil {
		t.Fatal(err)
	}
	ref := proto.AssignmentRef{SessionID: s.id, AssignmentID: uuid.UUID(s.binding.AssignmentID).String(), Epoch: s.binding.AssignmentEpoch}
	bind := proto.AssignmentBindPayload{EnvironmentID: s.configuration.EnvironmentID()}
	if bind.EnvironmentID != "" {
		r := s.binding.Resource
		bind.Resource = &sandboxbootstrap.Resource{TenantID: uuid.UUID(r.TenantID).String(), EnvironmentID: bind.EnvironmentID, Kind: "allocation",
			ID: uuid.UUID(r.ID).String(), Generation: r.Generation}
		bind.AttachGrant = s.binding.AttachGrant
		bind.WorkspaceDirectory = workspace
	}
	handle(t, router, ref, proto.TypeAssignmentBind, id, bind)
	var bound proto.AssignmentStatusPayload
	if err := out.next(t, id, time.After(turnLimit)).DecodePayload(&bound); err != nil || bound.State != proto.AssignmentBound {
		t.Fatalf("assignment_bind: %+v %v", bound, err)
	}
	return router, ref
}

// prepare applies each step to the Session's Environment with
// runtime_prepare, installs plugin, the archive of the selection's one plugin,
// unless it is nil, and finalizes the selection.
func (s *session) prepare(t *testing.T, plugin []byte, steps ...proto.RuntimeInitialization) {
	t.Helper()
	out := make(sender, 64)
	router, ref := s.router(t, out, uuid.NewString())
	defer router.Shutdown(context.Background())
	transfer := func(p proto.RuntimePreparePayload, data []byte) {
		p.Step, p.EnvironmentID, p.SessionID = "begin", s.configuration.EnvironmentID(), s.id
		frames := []proto.RuntimePreparePayload{p}
		if data != nil {
			digest := sha256.Sum256(data)
			frames[0].SizeBytes, frames[0].SHA256 = len(data), hex.EncodeToString(digest[:])
			frames = append(frames, proto.RuntimePreparePayload{Step: "chunk", Data: data})
		}
		id := uuid.NewString()
		for i, step := range append(frames, proto.RuntimePreparePayload{Step: "commit"}) {
			handle(t, router, ref, proto.TypeRuntimePrepare, id, step)
			var result proto.RuntimePrepareResultPayload
			if err := out.next(t, id, time.After(turnLimit)).DecodePayload(&result); err != nil {
				t.Fatal(err)
			}
			want := "received"
			if i == 0 {
				want = "ready"
			} else if i == len(frames) {
				want = "completed"
			}
			if result.Outcome != want {
				t.Fatalf("runtime_prepare %s %s: %+v, want %s", p.Action, step.Step, result, want)
			}
		}
	}
	for _, step := range steps {
		transfer(proto.RuntimePreparePayload{Action: "initialize", Initialization: &step}, nil)
	}
	sources := s.configuration.LocalEnvironment.CapabilitySources
	if plugin != nil {
		transfer(proto.RuntimePreparePayload{Action: "plugin", Plugin: &sources.Plugins[0]}, plugin)
	}
	transfer(proto.RuntimePreparePayload{Action: "finalize", Sources: sources}, nil)
}

// turn binds the Session's assignment, prepares an Executor of the Session,
// runs prompt as its Turn run, answers each function call with k's result,
// and retires the Executor with the Router's Shutdown. It returns the Turn's Done, its answer and its
// function calls.
func (s *session) turn(t *testing.T, run, prompt string, k ticket) (proto.DonePayload, string, []proto.FunctionCallPayload) {
	t.Helper()
	out := make(sender, 256)
	router, ref := s.router(t, out, "bind-"+run)
	prepare := "prepare-" + run
	handle(t, router, ref, proto.TypeExecutionPrepare, prepare, proto.ExecutionPreparePayload{SessionID: s.id, Configuration: s.configuration})
	ready := out.status(t, prepare)
	if ready.State != "ready" {
		t.Fatalf("the preparation is %s (%s), want ready; the log above says why", ready.State, ready.ErrorCode)
	}
	handle(t, router, ref, proto.TypeExecutionStart, prepare, proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID,
		RunID: run, Input: proto.TextInput(prompt)})
	done, answer, calls := out.collect(t, router, ref, run, k)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := router.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	return done, answer, calls
}

// checkCwd checks that the Harness's native state in the Session home names
// cwd, which the adapter writes into none of its files there.
func (s *session) checkCwd(t *testing.T, cwd string) {
	t.Helper()
	want := []byte(cwd)
	home := filepath.Join(s.cfg.StateDir, "sessions", s.binding.SessionID.String(), agent.ViewHomeName)
	var found string
	err := filepath.WalkDir(home, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || found != "" || !entry.Type().IsRegular() {
			return err
		}
		if body, err := os.ReadFile(name); err != nil || bytes.Contains(body, want) {
			found = name
			return err
		}
		return nil
	})
	if err != nil || found == "" {
		t.Errorf("no native state in %s names %s: %v", home, want, err)
		return
	}
	t.Logf("%s names %s", found, want)
}

// handle sends router a frame of ref's work.
func handle(t *testing.T, router *dispatch.Router, ref proto.AssignmentRef, typ, id string, payload any) {
	t.Helper()
	e, err := proto.NewEnvelope(typ, id, payload)
	if err != nil {
		t.Fatal(err)
	}
	e.Assignment = ref
	if err := router.Handle(context.Background(), e); err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
}

// sender holds what dispatch sends.
type sender chan proto.Envelope

func (s sender) Send(_ context.Context, e proto.Envelope) error {
	s <- e
	return nil
}

// next returns the next envelope with id.
func (s sender) next(t *testing.T, id string, deadline <-chan time.Time) proto.Envelope {
	t.Helper()
	for {
		select {
		case e := <-s:
			if e.ID == id {
				return e
			}
		case <-deadline:
			t.Fatalf("%s did not finish", id)
		}
	}
}

// status returns the preparation's first status other than preparing.
func (s sender) status(t *testing.T, id string) proto.PreparationStatusPayload {
	t.Helper()
	deadline := time.After(turnLimit)
	for {
		var p proto.PreparationStatusPayload
		if err := s.next(t, id, deadline).DecodePayload(&p); err != nil {
			t.Fatal(err)
		}
		if p.State != "preparing" {
			return p
		}
	}
}

// collect reads the Turn's envelopes until its Done. It answers each function
// call with k's result through router and checks that dispatch applied it.
// It checks that every delta names a started assistant message and every
// started message completes, and returns the completed messages' text as the
// answer.
func (s sender) collect(t *testing.T, router *dispatch.Router, ref proto.AssignmentRef, run string, k ticket) (proto.DonePayload, string, []proto.FunctionCallPayload) {
	t.Helper()
	deadline := time.After(turnLimit)
	var calls []proto.FunctionCallPayload
	var answer strings.Builder
	open := map[string]bool{}
	for {
		switch e := s.next(t, run, deadline); e.Type {
		case proto.TypeOutputMessage:
			var m proto.OutputMessagePayload
			if err := e.DecodePayload(&m); err != nil {
				t.Fatal(err)
			}
			if m.Status == "in_progress" {
				open[m.ID] = true
				continue
			}
			if !open[m.ID] || m.Text == nil {
				t.Errorf("message %q completed without a start or text: %s", m.ID, e.Payload)
				continue
			}
			delete(open, m.ID)
			t.Logf("message %s: %s", m.ID, *m.Text)
			answer.WriteString(*m.Text)
		case proto.TypeDelta:
			var d proto.DeltaPayload
			if err := e.DecodePayload(&d); err != nil {
				t.Fatal(err)
			}
			if !open[d.ItemID] {
				t.Errorf("a delta names no started message: %s", e.Payload)
			}
		case proto.TypeError:
			t.Errorf("Turn error: %s", e.Payload)
		case proto.TypeFunctionCall:
			var call proto.FunctionCallPayload
			if err := e.DecodePayload(&call); err != nil {
				t.Fatal(err)
			}
			t.Logf("function call: %s %s", call.Name, call.Arguments)
			calls = append(calls, call)
			handle(t, router, ref, proto.TypeFunctionResult, run, proto.FunctionResultPayload{DeliveryID: "result-" + call.CallID, CallID: call.CallID, Success: true, Content: k.result()})
		case proto.TypeInteractionDecisionAck:
			var ack proto.InteractionDecisionAckPayload
			if err := e.DecodePayload(&ack); err != nil || !ack.Applied {
				t.Errorf("a function result was not applied: %s", e.Payload)
			}
		case proto.TypeDone:
			var d proto.DonePayload
			if err := e.DecodePayload(&d); err != nil {
				t.Fatal(err)
			}
			if len(open) != 0 {
				t.Errorf("the Turn ended with incomplete messages: %v", open)
			}
			return d, answer.String(), calls
		}
	}
}

// sessionModel decodes the Harness's model and model_provider and adds the key.
func sessionModel(t *testing.T, raw string, key []byte) proto.PromptRequestPayload {
	t.Helper()
	var model proto.PromptRequestPayload
	if err := json.Unmarshal([]byte(raw), &model); err != nil || model.ModelProvider == nil {
		t.Fatalf("the model settings hold no model_provider: %v", err)
	}
	model.ModelProvider.APIKey = strings.TrimSpace(string(key))
	return model
}

// sandbox is the Link test relay that oac-sandbox-io in the sandbox
// container serves through.
type sandbox struct {
	auth     *sandboxlinktest.Authority
	url      string
	tls      *tls.Config
	resource sandboxlink.ResourceRef
}

func startSandbox(t *testing.T) *sandbox {
	auth := sandboxlinktest.NewAuthority()
	rl := relay.New(auth)
	srv := httptest.NewUnstartedServer(rl)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{testCA(t)}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	t.Cleanup(func() { rl.Close() })
	in := sandboxbootstrap.Input{Version: sandboxbootstrap.Version, LinkURL: "wss://" + strings.TrimPrefix(srv.URL, "https://"), Credential: "serve-" + rand.Text(),
		Resource: sandboxbootstrap.Resource{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), Kind: "allocation", ID: uuid.NewString(), Generation: 1}}
	raw, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	sb := &sandbox{auth: auth, url: in.LinkURL, tls: &tls.Config{RootCAs: roots}, resource: in.Resource.Ref()}
	auth.AddServe([]byte(in.Credential), sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: sb.resource})
	// The sandbox trusts the relay's CA through SSL_CERT_FILE, and reads the
	// bootstrap as the image's user.
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(runDir, "ca.pem"), ca, 0o644); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(runDir, ".bootstrap.json")
	if err := os.WriteFile(staged, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(staged, sandboxUser, sandboxUser); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, filepath.Join(runDir, "bootstrap.json")); err != nil {
		t.Fatal(err)
	}
	return sb
}

func (sb *sandbox) binding() agenthost.Binding {
	return agenthost.Binding{Resource: sb.resource, SessionID: sandboxwire.NewID(), AssignmentID: sandboxwire.NewID(), AssignmentEpoch: 1,
		AttachGrant: []byte("grant-" + rand.Text())}
}

func (sb *sandbox) grant(b agenthost.Binding, runtimeID sandboxwire.ID) {
	sb.auth.AddGrant(b.AttachGrant, sandboxlinktest.Grant{RuntimeID: runtimeID, Resource: b.Resource, SessionID: b.SessionID,
		AssignmentID: b.AssignmentID, AssignmentEpoch: b.AssignmentEpoch, Lease: time.Hour,
		Services: []sandboxlink.Service{sandboxlink.ServiceFile, sandboxlink.ServiceProcess, sandboxlink.ServiceNetwork}})
}

// files runs f on a File client of a fresh attachment.
func (sb *sandbox) files(t *testing.T, cfg agenthost.Config, f func(ctx context.Context, c *sandboxfs.Client, root sandboxfs.NodeRef)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b, attachment := sb.binding(), sandboxwire.NewID()
	sb.grant(b, cfg.RuntimeID)
	link, err := sandboxlink.DialAttach(ctx, sandboxlink.AttachConfig{URL: sb.url, TLS: cfg.TLS, RuntimeID: cfg.RuntimeID, Credential: cfg.Credential})
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	open := sandboxlink.Open{Service: sandboxlink.ServiceFile, Version: sandboxfs.Version, Resource: b.Resource, AttachmentID: attachment,
		SessionID: b.SessionID, AssignmentID: b.AssignmentID, AssignmentEpoch: b.AssignmentEpoch, AttachGrant: b.AttachGrant}
	st, _, err := link.OpenService(ctx, open)
	for errors.Is(err, sandboxlink.ServiceUnavailable) && ctx.Err() == nil {
		time.Sleep(100 * time.Millisecond)
		st, _, err = link.OpenService(ctx, open)
	}
	if err != nil {
		t.Fatalf("oac-sandbox-io does not serve File: %v", err)
	}
	c := sandboxfs.NewClient(st)
	defer c.Close()
	attached, err := c.Attach(ctx, &sandboxfs.AttachRequest{Export: sandboxfs.WorldExport})
	if err != nil {
		t.Fatal(err)
	}
	f(ctx, c, attached.Root.Node)
	if err := link.CloseAttachment(ctx, attachment); err != nil {
		t.Fatal(err)
	}
}

// ready waits until oac-sandbox-io serves the resource.
func (sb *sandbox) ready(t *testing.T, cfg agenthost.Config) {
	sb.files(t, cfg, func(context.Context, *sandboxfs.Client, sandboxfs.NodeRef) {})
}

// read returns the content of the sandbox file at name.
func (sb *sandbox) read(t *testing.T, cfg agenthost.Config, name string) (content string) {
	sb.files(t, cfg, func(ctx context.Context, c *sandboxfs.Client, root sandboxfs.NodeRef) {
		var names [][]byte
		for _, part := range strings.Split(strings.TrimPrefix(name, "/"), "/") {
			names = append(names, []byte(part))
		}
		walked, err := c.Walk(ctx, &sandboxfs.WalkRequest{Parent: root, Names: names})
		if err != nil {
			t.Fatal(err)
		}
		if walked.Failure != nil || len(walked.Entries) != len(names) {
			t.Errorf("%s is not in the sandbox: %v", name, walked.Failure)
			return
		}
		file := walked.Entries[len(walked.Entries)-1].Node
		if _, err := c.Open(ctx, &sandboxfs.OpenRequest{Handle: 1, Node: file, Access: sandboxfs.AccessRead}); err != nil {
			t.Fatal(err)
		}
		got, err := c.Read(ctx, &sandboxfs.ReadRequest{Handle: 1, Size: 4096})
		if err != nil {
			t.Fatal(err)
		}
		content = string(got.Data)
	})
	return content
}

// reset empties the sandbox's initialization area.
func (sb *sandbox) reset(t *testing.T, cfg agenthost.Config) {
	sb.files(t, cfg, func(ctx context.Context, c *sandboxfs.Client, root sandboxfs.NodeRef) {
		walked, err := c.Walk(ctx, &sandboxfs.WalkRequest{Parent: root, Names: [][]byte{[]byte("environment"), []byte("initialization")}})
		if err != nil || walked.Failure != nil {
			t.Fatalf("the initialization area: %v %v", err, walked.Failure)
		}
		empty(ctx, t, c, new(sandboxfs.HandleIDs), walked.Entries[1].Node)
	})
}

// empty removes what dir holds.
func empty(ctx context.Context, t *testing.T, c *sandboxfs.Client, handles *sandboxfs.HandleIDs, dir sandboxfs.NodeRef) {
	h := handles.Next()
	if _, err := c.OpenDir(ctx, &sandboxfs.OpenDirRequest{Handle: h, Node: dir}); err != nil {
		t.Fatal(err)
	}
	var entries []sandboxfs.DirEntry
	for cookie := uint64(0); ; {
		r, err := c.ReadDir(ctx, &sandboxfs.ReadDirRequest{Handle: h, Cookie: cookie, Limit: 64 << 10})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, r.Entries...)
		if r.End || len(r.Entries) == 0 {
			break
		}
		cookie = r.Entries[len(r.Entries)-1].Cookie
	}
	if _, err := c.ReleaseDir(ctx, &sandboxfs.ReleaseDirRequest{Handle: h}); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		var err error
		if e.Type == sandboxfs.ModeDirectory {
			var child *sandboxfs.LookupResponse
			if child, err = c.Lookup(ctx, &sandboxfs.LookupRequest{Parent: dir, Name: e.Name}); err == nil {
				empty(ctx, t, c, handles, child.Entry.Node)
				_, err = c.Rmdir(ctx, &sandboxfs.RmdirRequest{Parent: dir, Name: e.Name})
			}
		} else {
			_, err = c.Unlink(ctx, &sandboxfs.UnlinkRequest{Parent: dir, Name: e.Name})
		}
		if err != nil {
			t.Fatalf("remove %s: %v", e.Name, err)
		}
	}
}
