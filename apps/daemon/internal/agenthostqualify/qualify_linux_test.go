//go:build linux

// Package agenthostqualify runs one agent-host Session per real Harness
// through the daemon's dispatch against a sandbox container running
// oac-sandbox-io, with a real model behind the gateway.
package agenthostqualify

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// The rig is two privileged-suite style containers on the host network. The
// sandbox is plain Debian with bash, git, coreutils and ripgrep, running
// oac-sandbox-io against the Link test relay this test serves on loopback; the
// bootstrap reaches it through a shared run directory. The agent host runs this
// test with the pinned Harness installs, in a private cgroup namespace for the
// views' cgroups. Each OAC_QUALIFY_<KIND> holds the
// Harness's model and model_provider without api_key, which the test reads
// from OAC_QUALIFY_KEY_FILE.
//
//	CGO_ENABLED=0 go build -o $BIN/oac-sandbox-io ./apps/sandboxio/cmd/oac-sandbox-io
//	CGO_ENABLED=0 go build -o $BIN/oac-process-shim ./apps/daemon/cmd/oac-process-shim
//	CGO_ENABLED=0 go test -c -o $BIN/qualify.test ./apps/daemon/internal/agenthostqualify
//	printf 'FROM debian:bookworm-slim\nRUN apt-get update && apt-get install -y --no-install-recommends bash git coreutils ripgrep && mkdir -m 0777 /workspace\n' |
//	  docker build -t oac-qualify-sandbox -
//	RUN=$(mktemp -d)
//	docker run -d --rm --network host -v $RUN:/run/qualify:ro -v $BIN/oac-sandbox-io:/usr/local/bin/oac-sandbox-io:ro oac-qualify-sandbox \
//	  sh -c 'until [ -s /run/qualify/bootstrap.json ]; do sleep 1; done; exec oac-sandbox-io --bootstrap-file /run/qualify/bootstrap.json'
//	docker run --rm --network host --cgroupns=private --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
//	  -v $RUN:/run/qualify -v $BIN:/qualify:ro -v $KEY:/run/model.key:ro -v /etc/ssl/certs/ca-certificates.crt:/etc/oac-ca/ca.pem:ro \
//	  -v $CLAUDE_SDK:/opt/claude-sdk:ro -v $CODEX_BIN:/opt/codex:ro -v $MCODE_HARNESS:/opt/mcode-harness:ro \
//	  -e OAC_TEST_QUALIFY=1 -e OAC_QUALIFY_KEY_FILE=/run/model.key -e OAC_RUNTIME_HOME=/var/lib/oac \
//	  -e OAC_RUNTIME_CLAUDE_SDK_NODE=/usr/local/bin/node -e OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT=/opt/claude-sdk/dist/main.js \
//	  -e OAC_RUNTIME_CODEX_BIN=/opt/codex/codex \
//	  -e OAC_RUNTIME_MCODE_NODE=/usr/local/bin/node -e OAC_RUNTIME_MCODE_BIN=/opt/mcode-harness/native/cli.js \
//	  -e OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE=/opt/mcode-harness/bridge.mjs -e OAC_RUNTIME_MCODE_AGENTS_API=1 \
//	  -e OAC_QUALIFY_CLAUDE_SDK='{"model":"...","model_provider":{"protocol":"anthropic","base_url":"..."}}' \
//	  -e OAC_QUALIFY_CODEX='{"model":"...","model_provider":{"protocol":"responses","base_url":"..."}}' \
//	  -e OAC_QUALIFY_MCODE='{"model":"...","model_provider":{"protocol":"anthropic","base_url":"...","context_window":200000,"max_output_tokens":8192}}' \
//	  node:22.23.1-bookworm-slim /qualify/qualify.test -test.v -test.timeout 30m
const (
	gateEnv = "OAC_TEST_QUALIFY"
	keyEnv  = "OAC_QUALIFY_KEY_FILE"
	runDir  = "/run/qualify"
	shim    = "/qualify/oac-process-shim"
	caDir   = "/etc/oac-ca"
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
		t.Skipf("set %s=1 and run the test binary as the agent host of the two-container rig; see the comment above", gateEnv)
	}
	if err := sessionview.Probe(); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	key, err := os.ReadFile(os.Getenv(keyEnv))
	if err != nil {
		t.Fatalf("model key: %v", err)
	}
	sb := startSandbox(t)
	reg := agent.NewRegistry()
	cfg := agenthost.Config{StateDir: t.TempDir(), ViewCgroups: sessionviewtest.CgroupParent(t), UIDs: agenthost.UIDRange{First: 70000, Count: 8}, RelayURL: sb.url,
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
				t.Skipf("set OAC_QUALIFY_%s to the Harness's model options", strings.ToUpper(kind))
			}
			runtime := declaration.Discover(context.Background(), agent.DiscoveryOptions{Profile: "default", Stdout: io.Discard, Stderr: os.Stderr}, declaration.Info)
			if runtime == nil || runtime.View == nil {
				t.Fatalf("%s declares no agent-host view; discovery reported why above", kind)
			}
			reg.Register(declaration, *runtime)
			qualify(t, h, cfg, sb, kind, sessionOptions(t, raw, key))
		})
	}
}

// qualify runs one Turn that writes a file, runs a failing command and
// reports what it printed and its exit status, then checks all three. Only
// the sandbox's tool environment holds the value and the status.
func qualify(t *testing.T, h *agenthost.Host, cfg agenthost.Config, sb *sandbox, kind string, options map[string]any) {
	name := "qualify-" + kind + ".txt"
	value, content := strings.ToLower(rand.Text()), "qualified "+strings.ToLower(rand.Text()[:12])
	code, _ := rand.Int(rand.Reader, big.NewInt(90))
	exit := code.Int64() + 3
	prompt := "Use your tools for each step.\n" +
		"1. Create the file " + name + " in the current directory with exactly this content: " + content + "\n" +
		"2. Run this shell command exactly once: echo \"$QUALIFY_VALUE\"; exit \"$QUALIFY_EXIT\"\n" +
		"   It prints a value and exits with a non-zero status; that is expected.\n" +
		"3. Answer with exactly one line: VALUE=<the printed value> EXIT=<the exit status>"

	// dispatch drives the Session's Turn through the agent host's Executor.
	b := sb.binding()
	sb.grant(b, cfg.RuntimeID)
	env := agenthost.Environment{
		Sandbox: map[string]string{"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME": "/root", "LANG": "C.UTF-8"},
		Tool:    map[string]string{"QUALIFY_VALUE": value, "QUALIFY_EXIT": fmt.Sprint(exit)},
	}
	out := make(sender, 256)
	router, err := dispatch.New(dispatch.Config{Sender: out, SessionEnvironments: true, Log: cfg.Log,
		Registry: h.Registry(func(proto.PromptRequestPayload) (agenthost.Binding, agenthost.Environment, error) { return b, env, nil })})
	if err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	handle(t, router, proto.TypeExecutionPrepare, "prepare", proto.ExecutionPreparePayload{SessionID: session, Configuration: proto.PromptRequestPayload{
		AgentKind: kind, AgentStateKey: "agents-api-" + session, StrictResume: true, DisableSubagents: true,
		AgentOptions: options, ExecutionControls: &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"},
		LocalEnvironment: &proto.LocalEnvironment{WorkspaceDirectory: workspace, NetworkAccess: "enabled"}}})
	ready := out.status(t, "prepare")
	if ready.State != "ready" {
		t.Fatalf("the preparation is %s (%s), want ready; the log above says why", ready.State, ready.ErrorCode)
	}
	handle(t, router, proto.TypeExecutionStart, "prepare", proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID,
		RunID: "qualify", Input: proto.TextInput(prompt)})
	answer := out.collect(t, "qualify")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := router.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}

	if !strings.Contains(answer, "VALUE="+value) || !strings.Contains(answer, fmt.Sprintf("EXIT=%d", exit)) {
		t.Errorf("the answer %q does not report VALUE=%s EXIT=%d", answer, value, exit)
	}
	if got := sb.read(t, cfg, workspace+"/"+name); strings.TrimRight(got, "\n") != content {
		t.Errorf("%s holds %q, want %q", name, got, content)
	}
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

// collect reads the Turn's envelopes until its Done and returns its content.
func (s sender) collect(t *testing.T, run string) string {
	t.Helper()
	deadline := time.After(turnLimit)
	for {
		switch e := s.next(t, run, deadline); e.Type {
		case proto.TypeError:
			t.Errorf("Turn error: %s", e.Payload)
		case proto.TypeDone:
			var d proto.DonePayload
			if err := json.Unmarshal(e.Payload, &d); err != nil {
				t.Fatal(err)
			}
			t.Logf("answer: %s", d.Content)
			return d.Content
		}
	}
}

// sessionOptions decodes the Harness's model options and adds the key.
func sessionOptions(t *testing.T, raw string, key []byte) map[string]any {
	t.Helper()
	var options map[string]any
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		t.Fatalf("model options: %v", err)
	}
	provider, ok := options["model_provider"].(map[string]any)
	if !ok {
		t.Fatal("model options hold no model_provider")
	}
	provider["api_key"] = strings.TrimSpace(string(key))
	return options
}

// sandbox is the Link test relay that oac-sandbox-io in the sandbox
// container serves through.
type sandbox struct {
	auth     *sandboxlinktest.Authority
	url      string
	resource sandboxlink.ResourceRef
}

func startSandbox(t *testing.T) *sandbox {
	auth := sandboxlinktest.NewAuthority()
	rl := relay.New(auth)
	srv := httptest.NewServer(rl)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { rl.Close() })
	in := sandboxbootstrap.Input{Version: sandboxbootstrap.Version, LinkURL: "ws://" + strings.TrimPrefix(srv.URL, "http://"), Credential: "serve-" + rand.Text(),
		Resource: sandboxbootstrap.Resource{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), Kind: "allocation", ID: uuid.NewString(), Generation: 1}}
	raw, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	sb := &sandbox{auth: auth, url: in.LinkURL, resource: in.Resource.Ref()}
	auth.AddServe([]byte(in.Credential), sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: sb.resource})
	staged := filepath.Join(runDir, ".bootstrap.json")
	if err := os.WriteFile(staged, raw, 0o600); err != nil {
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
	link, err := sandboxlink.DialAttach(ctx, sandboxlink.AttachConfig{URL: sb.url, RuntimeID: cfg.RuntimeID, Credential: cfg.Credential})
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
