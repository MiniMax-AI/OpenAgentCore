//go:build linux

package agenthost

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// The test binary is also the privileged suite's Harness inside the view and
// its process with a zombie leader.
func TestMain(m *testing.M) {
	sessionview.Init()
	if os.Getenv(harnessEnv) != "" {
		os.Exit(runHarness(os.Args[1:]))
	}
	if os.Getenv(zombieLeaderEnv) != "" {
		runZombieLeader()
	}
	os.Exit(m.Run())
}

// newConfig returns a Config whose CA directory holds ca.
func newConfig(t *testing.T, reg *agent.Registry, ca *x509.Certificate) Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{StateDir: t.TempDir(), UIDs: UIDRange{First: 70000, Count: 8}, RelayURL: "ws://127.0.0.1:9", RuntimeID: sandboxwire.NewID(),
		Credential: []byte("runtime-credential"), Harnesses: reg, Shim: exe, CADir: dir}
}

// register declares kind with view, or without one when view is nil.
func register(reg *agent.Registry, kind string, view *agent.View, connection ...string) {
	info := proto.SupportedAgentKind{Kind: kind, Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}
	declaration := agent.Declaration{Info: info, ConnectionOptions: connection,
		Configuration: harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: string(modelprovider.Anthropic)}}}}
	reg.Register(declaration, agent.Runtime{Info: info, View: view,
		Session: func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, errors.New("not used")
		}})
}

// request is a Session request the agent host admits.
func request(kind, workspace, baseURL, key string) proto.PromptRequestPayload {
	return proto.PromptRequestPayload{
		AgentKind:        kind,
		StrictResume:     true,
		AgentOptions:     map[string]any{"model": "m", "model_provider": map[string]any{"protocol": "anthropic", "base_url": baseURL, "api_key": key}},
		LocalEnvironment: &proto.LocalEnvironment{WorkspaceRoot: workspace, NetworkAccess: "enabled"},
	}
}

// newSession returns a Session for req on a fresh attachment of resource.
func newSession(resource sandboxlink.ResourceRef, req proto.PromptRequestPayload) (Session, chan Input, chan proto.Envelope) {
	in, out := make(chan Input), make(chan proto.Envelope, 16)
	return Session{
		Binding: Binding{Resource: resource, AttachmentID: sandboxwire.NewID(), SessionID: sandboxwire.NewID(),
			AssignmentID: sandboxwire.NewID(), AssignmentEpoch: 1, AttachGrant: []byte("grant-" + sandboxwire.NewID().String())},
		Request: req, Input: in, Output: out,
	}, in, out
}

func newResource() sandboxlink.ResourceRef {
	return sandboxlink.ResourceRef{TenantID: sandboxwire.NewID(), EnvironmentID: sandboxwire.NewID(), Kind: sandboxlink.ResourceAllocation,
		ID: sandboxwire.NewID(), Generation: 1}
}

// countingDial counts dials and connects nothing.
func countingDial(n *atomic.Int32) dialFunc {
	return func(context.Context, func(sandboxlink.AttachmentClosed)) (attachLink, error) {
		n.Add(1)
		return nil, errors.New("no relay in this test")
	}
}

// noBroker is a process broker that serves nothing.
type noBroker struct{}

func (noBroker) Start(brokerConfig) error { return nil }
func (noBroker) Close() error             { return nil }

// leftSessions lists what remains under the state directory's sessions.
func leftSessions(t *testing.T, cfg Config) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(sessionsDir(cfg.StateDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return entries
}

// fakeProcesses is a process table that lists fixed tasks.
type fakeProcesses struct {
	list []task
}

func (f *fakeProcesses) tasks() ([]task, error) { return f.list, nil }
