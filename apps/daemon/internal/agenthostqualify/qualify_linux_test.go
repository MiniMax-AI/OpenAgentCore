//go:build linux

// Package agenthostqualify runs one agent-host Session per real Harness
// through the daemon's dispatch against a sandbox container running
// oac-sandbox-io, with a real model behind the gateway.
package agenthostqualify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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
	workspace = "/workspace"
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
			runtime := declaration.Discover(context.Background(), agent.DiscoveryOptions{Profile: "default", Stdout: io.Discard, Stderr: os.Stderr}, declaration.Info)
			if runtime == nil || runtime.View == nil {
				t.Fatalf("%s declares no agent-host view; discovery reported why above", kind)
			}
			reg.Register(declaration, *runtime)
			qualify(t, h, cfg, sb, kind, runtime.View.Capabilities, sessionModel(t, raw, key))
		})
	}
}

// qualify runs the kind's Turns through dispatch. The first writes a file,
// runs a failing command and reports what it printed and its exit status,
// then the test checks all three; only the sandbox's tool environment holds
// the value and the status. A view that declares function tools runs a second
// Turn in a new Executor, which resumes the Session's native history, and
// calls a function there. A view that declares tool search runs a Turn in
// another Session that finds the function, deferred, with tool search. A view
// that declares environment none answers a Turn in a Session without an
// Environment, and its native state names the work directory.
// A view that declares stdio MCP calls a tool of a stdio MCP server that runs
// in the sandbox.
func qualify(t *testing.T, h *agenthost.Host, cfg agenthost.Config, sb *sandbox, kind string, caps agent.ViewCapabilities, model proto.PromptRequestPayload) {
	name := "qualify-" + kind + ".txt"
	value, content := strings.ToLower(rand.Text()), "qualified "+strings.ToLower(rand.Text()[:12])
	code, _ := rand.Int(rand.Reader, big.NewInt(90))
	exit := code.Int64() + 3
	prompt := "Use your tools for each step.\n" +
		"1. Create the file " + name + " in the current directory with exactly this content: " + content + "\n" +
		"2. Run this shell command exactly once: echo \"$QUALIFY_VALUE\"; exit \"$QUALIFY_EXIT\"\n" +
		"   It prints a value and exits with a non-zero status; that is expected.\n" +
		"3. Answer with exactly one line: VALUE=<the printed value> EXIT=<the exit status>"

	env := agenthost.Environment{
		Sandbox: map[string]string{"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME": "/home/runtime", "LANG": "C.UTF-8"},
		Tool:    map[string]string{"QUALIFY_VALUE": value, "QUALIFY_EXIT": fmt.Sprint(exit)},
	}
	configuration := proto.PromptRequestPayload{AgentKind: kind, DisableSubagents: true,
		Model: model.Model, ModelProvider: model.ModelProvider, ExecutionControls: &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"},
		LocalEnvironment: &proto.LocalEnvironment{WorkspaceDirectory: workspace, NetworkAccess: "enabled"}}
	if caps.FunctionTools.IsSupported() {
		configuration.FunctionTools = []proto.FunctionTool{lookupTicket}
	}
	k := newTicket(t)
	s := sb.session(h, cfg, env, configuration)
	done, _ := s.turn(t, "qualify", prompt, k)
	if !strings.Contains(done.Content, "VALUE="+value) || !strings.Contains(done.Content, fmt.Sprintf("EXIT=%d", exit)) {
		t.Errorf("the answer %q does not report VALUE=%s EXIT=%d", done.Content, value, exit)
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
		s.configuration.AgentSessionID = native
		done, calls := s.turn(t, "resumed-function", k.prompt("Call the lookup_ticket function"), k)
		if resumed, _ := done.Metadata[proto.DoneMetaAgentSessionID].(string); resumed != native {
			t.Errorf("the resumed Turn reported the native session %q, want %q", resumed, native)
		}
		k.check(t, done, calls)
	}
	if caps.ToolSearch.IsSupported() {
		deferred := lookupTicket
		deferred.DeferLoading = true
		configuration.ToolSearch, configuration.FunctionTools = true, []proto.FunctionTool{deferred}
		search := sb.session(h, cfg, env, configuration)
		done, calls := search.turn(t, "tool-search", k.prompt("Search your tools for the function that looks up support tickets"), k)
		k.check(t, done, calls)
	}
	if caps.EnvironmentNone.IsSupported() {
		none := configuration
		none.LocalEnvironment, none.DisableExecutionEnvironment, none.FunctionTools, none.ToolSearch = nil, true, nil, false
		s := sb.session(h, cfg, agenthost.Environment{}, none)
		done, _ := s.turn(t, "environment-none", "What is 17 times 23? Answer with exactly one line: PRODUCT=<the number>", k)
		if !strings.Contains(done.Content, "PRODUCT=391") {
			t.Errorf("the answer %q does not report PRODUCT=391", done.Content)
		}
		s.checkCwd(t, agent.ViewPrivateRoot+"/"+agent.ViewHomeName+"/"+agent.ViewWorkName)
	}
	if caps.StdioMCP.IsSupported() {
		code := strings.ToLower(rand.Text()[:12])
		stdio := configuration
		stdio.FunctionTools, stdio.ToolSearch = nil, false
		s := sb.session(h, cfg, env, stdio)
		s.mcp = []proto.EnvironmentMCP{{InstallationRoot: workspace, Server: agentplugin.MCPServer{Name: "qualify", Type: "stdio", Command: "python3", Args: []string{"-c", mcpServer, code}}}}
		done, _ := s.turn(t, "stdio-mcp", "Call the reveal_code tool of the qualify MCP server once.\nAnswer with exactly one line: CODE=<the code it returns>", k)
		if !strings.Contains(done.Content, "CODE="+code) {
			t.Errorf("the answer %q does not report CODE=%s", done.Content, code)
		}
	}
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
        reply["result"] = {"tools": [{"name": "reveal_code", "description": "Returns the qualification code.", "inputSchema": {"type": "object", "properties": {}}}]}
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
func (k ticket) check(t *testing.T, done proto.DonePayload, calls []proto.FunctionCallPayload) {
	t.Helper()
	if len(calls) == 0 || calls[0].Name != lookupTicket.Name || !strings.Contains(string(calls[0].Arguments), k.number) {
		t.Errorf("the Turn made %d function calls, want the first to call %s for ticket %s", len(calls), lookupTicket.Name, k.number)
	}
	if !strings.Contains(done.Content, "FIRST="+k.first) || !strings.Contains(done.Content, "SECOND="+k.second) {
		t.Errorf("the answer %q does not report FIRST=%s SECOND=%s", done.Content, k.first, k.second)
	}
}

// session is a Session bound to the sandbox. Each Turn runs in a new
// Executor through a new dispatch Router.
type session struct {
	h             *agenthost.Host
	cfg           agenthost.Config
	binding       agenthost.Binding
	env           agenthost.Environment
	id            string
	configuration proto.PromptRequestPayload
	// mcp is the installed MCP that the Environment's preparation resolves
	// into each request; the wire does not carry it.
	mcp []proto.EnvironmentMCP
}

func (sb *sandbox) session(h *agenthost.Host, cfg agenthost.Config, env agenthost.Environment, configuration proto.PromptRequestPayload) *session {
	s := &session{h: h, cfg: cfg, binding: sb.binding(), env: env, id: uuid.NewString(), configuration: configuration}
	s.configuration.AgentStateKey = "agents-api-" + s.id
	sb.grant(s.binding, cfg.RuntimeID)
	return s
}

// turn prepares an Executor of the Session, runs prompt as its Turn run,
// answers each function call with k's result, and retires the Executor with
// the Router's Shutdown. It returns the Turn's Done and its function calls.
func (s *session) turn(t *testing.T, run, prompt string, k ticket) (proto.DonePayload, []proto.FunctionCallPayload) {
	t.Helper()
	out := make(sender, 256)
	reg := s.h.Registry(func(proto.PromptRequestPayload) (agenthost.Binding, agenthost.Environment, error) {
		return s.binding, s.env, nil
	})
	if s.mcp != nil {
		reg = withMCP(t, reg, s.mcp)
	}
	router, err := dispatch.New(dispatch.Config{Sender: out, SessionEnvironments: true, Log: s.cfg.Log, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	prepare := "prepare-" + run
	handle(t, router, proto.TypeExecutionPrepare, prepare, proto.ExecutionPreparePayload{SessionID: s.id, Configuration: s.configuration})
	ready := out.status(t, prepare)
	if ready.State != "ready" {
		t.Fatalf("the preparation is %s (%s), want ready; the log above says why", ready.State, ready.ErrorCode)
	}
	handle(t, router, proto.TypeExecutionStart, prepare, proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID,
		RunID: run, Input: proto.TextInput(prompt)})
	done, calls := out.collect(t, router, run, k)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := router.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	return done, calls
}

// withMCP wraps reg so that each request's Environment carries mcp.
func withMCP(t *testing.T, reg *agent.Registry, mcp []proto.EnvironmentMCP) *agent.Registry {
	wrapped := agent.NewRegistry()
	for _, info := range reg.SupportedAgentKinds() {
		configuration, err := reg.Configuration(info.Kind)
		direct, directErr := reg.Resolve(info.Kind)
		factory, factoryErr := reg.ResolveExecutor(info.Kind)
		if err := errors.Join(err, directErr, factoryErr); err != nil {
			t.Fatal(err)
		}
		wrapped.RegisterKind(info, configuration, direct)
		wrapped.RegisterExecutor(info.Kind, func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
			local := *req.LocalEnvironment
			local.MCP = mcp
			req.LocalEnvironment = &local
			return factory(ctx, req)
		})
	}
	return wrapped
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

func handle(t *testing.T, router *dispatch.Router, typ, id string, payload any) {
	t.Helper()
	e, err := proto.NewEnvelope(typ, id, payload)
	if err != nil {
		t.Fatal(err)
	}
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
func (s sender) collect(t *testing.T, router *dispatch.Router, run string, k ticket) (proto.DonePayload, []proto.FunctionCallPayload) {
	t.Helper()
	deadline := time.After(turnLimit)
	var calls []proto.FunctionCallPayload
	for {
		switch e := s.next(t, run, deadline); e.Type {
		case proto.TypeError:
			t.Errorf("Turn error: %s", e.Payload)
		case proto.TypeFunctionCall:
			var call proto.FunctionCallPayload
			if err := e.DecodePayload(&call); err != nil {
				t.Fatal(err)
			}
			t.Logf("function call: %s %s", call.Name, call.Arguments)
			calls = append(calls, call)
			handle(t, router, proto.TypeFunctionResult, run, proto.FunctionResultPayload{DeliveryID: "result-" + call.CallID, CallID: call.CallID, Success: true, Content: k.result()})
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
			t.Logf("answer: %s", d.Content)
			return d, calls
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
