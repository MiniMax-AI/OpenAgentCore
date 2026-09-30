package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

// fakeConn is the WSConn implementation used by session + registry
// tests. Concurrency-safe.
type fakeConn struct {
	mu       sync.Mutex
	incoming []fakeFrame
	cond     *sync.Cond
	closed   bool

	writes   [][]byte
	writeErr error
}

type fakeFrame struct {
	data []byte
	err  error
}

func newFakeConn() *fakeConn {
	c := &fakeConn{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *fakeConn) Feed(data []byte) {
	c.mu.Lock()
	c.incoming = append(c.incoming, fakeFrame{data: data})
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *fakeConn) FeedError(err error) {
	c.mu.Lock()
	c.incoming = append(c.incoming, fakeFrame{err: err})
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *fakeConn) ReadMessage() (int, []byte, error) {
	c.mu.Lock()
	for len(c.incoming) == 0 && !c.closed {
		c.cond.Wait()
	}
	if c.closed && len(c.incoming) == 0 {
		c.mu.Unlock()
		return 0, nil, io.EOF
	}
	f := c.incoming[0]
	c.incoming = c.incoming[1:]
	c.mu.Unlock()
	if f.err != nil {
		return 0, nil, f.err
	}
	return 1, f.data, nil
}

func (c *fakeConn) WriteMessage(_ int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr != nil {
		return c.writeErr
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	c.writes = append(c.writes, cp)
	return nil
}

func (c *fakeConn) SetReadLimit(int64)              {}
func (c *fakeConn) SetReadDeadline(time.Time) error { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	return nil
}

func (c *fakeConn) Writes() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.writes))
	copy(out, c.writes)
	return out
}

type fakeHeartbeatStore struct {
	mu       sync.Mutex
	daemonCh chan device.Heartbeat
	daemon   []device.Heartbeat
	runtime  []string
}

func newFakeHeartbeatStore() *fakeHeartbeatStore {
	return &fakeHeartbeatStore{daemonCh: make(chan device.Heartbeat, 4)}
}

func (f *fakeHeartbeatStore) TouchRuntimeHeartbeat(_ context.Context, runtimeID string) (device.HeartbeatStatus, error) {
	f.mu.Lock()
	f.runtime = append(f.runtime, runtimeID)
	f.mu.Unlock()
	return device.HeartbeatStatus{Liveness: "online"}, nil
}

func (f *fakeHeartbeatStore) TouchAgentDaemonHeartbeat(_ context.Context, input device.Heartbeat) (device.HeartbeatStatus, error) {
	f.mu.Lock()
	f.daemon = append(f.daemon, input)
	f.mu.Unlock()
	select {
	case f.daemonCh <- input:
	default:
	}
	return device.HeartbeatStatus{Liveness: "online"}, nil
}

func (f *fakeHeartbeatStore) MarkRuntimeOffline(_ context.Context, _ string) error {
	return nil
}

func (f *fakeHeartbeatStore) waitDaemonHeartbeat(t *testing.T) device.Heartbeat {
	t.Helper()
	select {
	case input := <-f.daemonCh:
		return input
	case <-time.After(2 * time.Second):
		t.Fatal("daemon heartbeat was not persisted")
	}
	return device.Heartbeat{}
}

// ---- registry tests -------------------------------------------------

func TestRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewRegistry()
	sess := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	if prev := reg.Register(sess); prev != nil {
		t.Fatalf("first Register returned non-nil prev")
	}
	got, err := reg.LookupDevice("dev-1")
	if err != nil || got != sess {
		t.Fatalf("LookupDevice round-trip failed: got=%p err=%v", got, err)
	}
}

func TestRegistry_RegisterReplacesAndEvictsRuns(t *testing.T) {
	reg := NewRegistry()
	old := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	reg.Register(old)
	reg.AttachRun("run-1", old)
	reg.AttachPermission("perm-1", old)

	new := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	prev := reg.Register(new)
	if prev != old {
		t.Fatalf("expected old session as displaced previous, got %p", prev)
	}
	// Old session's run/perm indexes must be cleared so a stale
	// Cancel can't be routed to the wrong session.
	if got := reg.LookupRun("run-1"); got != nil {
		t.Fatalf("expected run-1 mapping cleared, got %p", got)
	}
	if _, err := reg.LookupPermission("perm-1"); !errors.Is(err, ErrPermissionNotRegistered) {
		t.Fatalf("expected perm-1 cleared, got %v", err)
	}
}

func TestRegistry_DeregisterPreservesNewer(t *testing.T) {
	reg := NewRegistry()
	old := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	reg.Register(old)
	new := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	reg.Register(new)
	// A stale Deregister from the old session (e.g. its read loop
	// wakes after preemption) must NOT remove the new session.
	reg.Deregister(old)
	got, err := reg.LookupDevice("dev-1")
	if err != nil || got != new {
		t.Fatalf("new session evicted by stale Deregister: got=%p err=%v", got, err)
	}
}

// ---- session tests --------------------------------------------------

func TestSession_DispatchDeliversToSubscriber(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Start()
	defer sess.Close("test done")

	sub, err := sess.SubscribeDurable("run-1")
	ch := sub.Events
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	env, _ := proto.NewEnvelope(proto.TypeDelta, "run-1", proto.DeltaPayload{Delta: "hi", Sequence: 1})
	raw, _ := jsonMarshal(env)
	conn.Feed(raw)

	select {
	case got := <-ch:
		if got.Type != proto.TypeDelta || got.ID != "run-1" {
			t.Fatalf("unexpected env: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber never received delta")
	}
}

func TestSession_DoneFrameAutoUnsubscribes(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Start()
	defer sess.Close("test done")

	sub, _ := sess.SubscribeDurable("run-1")
	ch := sub.Events
	env, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "ok"})
	raw, _ := jsonMarshal(env)
	conn.Feed(raw)

	// Drain the done, then expect the channel to be closed by the
	// auto-unsubscribe path.
	deadline := time.After(2 * time.Second)
	gotDone := false
	for {
		select {
		case env, ok := <-ch:
			if !ok {
				if !gotDone {
					t.Fatal("channel closed without delivering done envelope")
				}
				return
			}
			if env.Type == proto.TypeDone {
				gotDone = true
			}
		case <-deadline:
			t.Fatal("subscriber channel never closed after done")
		}
	}
}

func TestSession_PermissionRequestIndexedInRegistry(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Start()
	defer sess.Close("test done")

	sub, err := sess.SubscribeDurable("run-1")
	ch := sub.Events
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	env, _ := proto.NewEnvelope(proto.TypePermissionRequest, "run-1", proto.PermissionRequestPayload{
		RequestID: "perm-abc",
		Tool:      "Bash",
		Title:     "rm -rf /tmp/scratch",
	})
	raw, _ := jsonMarshal(env)
	conn.Feed(raw)

	// Poll because the read loop is async.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, err := reg.LookupPermission("perm-abc"); err == nil && got == sess {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("perm-abc never indexed in registry")
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case got := <-ch:
		if got.ID != "run-1" || got.Type != proto.TypePermissionRequest {
			t.Fatalf("subscriber received %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run subscriber never received permission request")
	}
}

func TestSession_CloseReportsUnknownWithoutExecutionEvents(t *testing.T) {
	sess := NewSession(newFakeConn(), "device", "tenant", proto.Version, NewRegistry(), nil)
	sub, err := sess.SubscribeDurable("run")
	if err != nil {
		t.Fatal(err)
	}
	sess.Close("simulated drop")
	for env := range sub.Events {
		t.Fatalf("disconnect fabricated execution event: %s", env.Type)
	}
	if !errors.Is(sub.Err(), ErrSessionClosed) {
		t.Fatalf("missing transport error: %v", sub.Err())
	}
}

func TestSession_NoCapabilitiesBeforeHeartbeat(t *testing.T) {
	sess := NewSession(newFakeConn(), "device", "tenant", proto.Version, NewRegistry(), nil)
	defer sess.Close("test done")
	for _, kind := range []string{"fake_alpha", "codex", "contract"} {
		if info, found, known := sess.AgentKindStatus(kind); found || known || info.Available {
			t.Fatalf("unadvertised engine was available: %+v", info)
		}
	}
}

func TestSession_SendOnClosedReturnsError(t *testing.T) {
	reg := NewRegistry()
	sess := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Close("never started")
	env, _ := proto.NewEnvelope(proto.TypePromptCancel, "run-1", nil)
	err := sess.Send(context.Background(), env)
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed, got %v", err)
	}
}

func TestSession_SendWritesToWire(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Start()
	defer sess.Close("test done")

	env, _ := proto.NewEnvelope(proto.TypePromptRequest, "run-1", proto.PromptRequestPayload{
		AgentKind: "fake_alpha",
		RunID:     "run-1",
		Input:     proto.TextInput("hello"),
	})
	if err := sess.Send(context.Background(), env); err != nil {
		t.Fatalf("Send: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(conn.Writes()) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("write loop never flushed envelope to wire")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSession_HeartbeatPersistsSupportedAgentKinds(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	heartbeat := newFakeHeartbeatStore()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.heartbeat = heartbeat
	sess.Start()
	defer sess.Close("test done")

	env, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{
		Timestamp:      1710000000,
		ActiveRequests: 2,
		DaemonVersion:  "0.2.0-test",
		SupportedAgentKinds: []proto.SupportedAgentKind{
			{
				Kind:      "fake_beta",
				Available: false,
				Version:   "missing",
				Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
					Streaming: proto.CapabilitySupported,
				}),
			},
			{
				Kind:      "fake_alpha",
				Available: true,
				Version:   "1.2.3",
				Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
					Streaming:   proto.CapabilitySupported,
					Permissions: proto.CapabilitySupported,
					Usage:       proto.CapabilitySupported,
					Resume:      proto.CapabilitySupported,
				}),
			},
			{
				Kind:         "codex",
				Available:    true,
				Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{MCPHTTPTools: proto.CapabilitySupported, Steering: proto.CapabilitySupported, MessageItems: proto.CapabilitySupported, ToolObservations: proto.CapabilitySupported, EnvironmentNone: proto.CapabilitySupported, WebSearchControl: proto.CapabilitySupported, TextVerbosity: proto.CapabilitySupported, ExecutionControls: proto.CapabilitySupported, SubagentControl: proto.CapabilitySupported}),
			},
		},
	})
	raw, _ := jsonMarshal(env)
	conn.Feed(raw)

	got := heartbeat.waitDaemonHeartbeat(t)
	if got.RuntimeID != "dev-1" || got.DaemonVersion != "0.2.0-test" || got.ActiveRequests != 2 || got.HeartbeatTimestamp != 1710000000 {
		t.Fatalf("heartbeat metadata not preserved: %+v", got)
	}
	if len(got.SupportedAgentKinds) != 3 {
		t.Fatalf("SupportedAgentKinds len = %d, want 3: %#v", len(got.SupportedAgentKinds), got.SupportedAgentKinds)
	}
	byKind := map[string]device.SupportedAgentKind{}
	for _, info := range got.SupportedAgentKinds {
		byKind[info.Kind] = info
	}
	claude := byKind["fake_alpha"]
	if !claude.Available || claude.Version != "1.2.3" || !claude.Capabilities.Permissions || !claude.Capabilities.Usage || !claude.Capabilities.Resume {
		t.Fatalf("fake_alpha descriptor not converted: %#v", claude)
	}
	fake_beta := byKind["fake_beta"]
	if fake_beta.Available || fake_beta.Version != "missing" || !fake_beta.Capabilities.Streaming {
		t.Fatalf("fake_beta descriptor not converted: %#v", fake_beta)
	}
	if !byKind["codex"].Capabilities.ExecutionControls || claude.Capabilities.ExecutionControls || fake_beta.Capabilities.ExecutionControls || !byKind["codex"].Capabilities.ToolObservations || claude.Capabilities.ToolObservations || fake_beta.Capabilities.ToolObservations || !byKind["codex"].Capabilities.SubagentControl || claude.Capabilities.SubagentControl || fake_beta.Capabilities.SubagentControl || !byKind["codex"].Capabilities.TextVerbosity || claude.Capabilities.TextVerbosity || fake_beta.Capabilities.TextVerbosity || !byKind["codex"].Capabilities.WebSearchControl || claude.Capabilities.WebSearchControl || fake_beta.Capabilities.WebSearchControl || !byKind["codex"].Capabilities.EnvironmentNone || claude.Capabilities.EnvironmentNone || fake_beta.Capabilities.EnvironmentNone || !byKind["codex"].Capabilities.MessageItems || !byKind["codex"].Capabilities.Steering || claude.Capabilities.Steering || fake_beta.Capabilities.Steering {
		t.Fatalf("steering capability not preserved: %#v", byKind)
	}
	if !byKind["codex"].Capabilities.MCPHTTPTools || claude.Capabilities.MCPHTTPTools || fake_beta.Capabilities.MCPHTTPTools {
		t.Fatalf("HTTP MCP capability not preserved: %#v", byKind)
	}
	codex, found, known := sess.AgentKindStatus("codex")
	if !found || !known || !codex.Capabilities.Steering || !codex.Capabilities.MCPHTTPTools {
		t.Fatalf("steering capability absent from live session: %#v", codex)
	}
}

func TestSession_HeartbeatDoesNotInferCapabilities(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	heartbeat := newFakeHeartbeatStore()
	sess := NewSession(conn, "device", "tenant", proto.Version, reg, nil)
	sess.heartbeat = heartbeat
	sess.Start()
	defer sess.Close("test done")

	conn.Feed([]byte(`{"type":"heartbeat","payload":{"ts":1710000100,"claude_available":true}}`))
	got := heartbeat.waitDaemonHeartbeat(t)
	if len(got.SupportedAgentKinds) != 0 {
		t.Fatalf("undeclared capabilities inferred: %#v", got.SupportedAgentKinds)
	}
}

func TestSession_PermissionRequiresPayloadIdentity(t *testing.T) {
	reg := NewRegistry()
	sess := NewSession(newFakeConn(), "device", "tenant", proto.Version, reg, nil)
	defer sess.Close("test done")
	sub, err := sess.SubscribeDurable("run")
	if err != nil {
		t.Fatal(err)
	}
	env, _ := proto.NewEnvelope(proto.TypePermissionRequest, "run", proto.PermissionRequestPayload{Tool: "test"})
	sess.dispatch(env)
	if _, err := reg.LookupPermission("run"); !errors.Is(err, ErrPermissionNotRegistered) {
		t.Fatal("run ID was treated as an interaction ID")
	}
	if len(sub.Events) != 0 {
		t.Fatal("invalid permission request was forwarded")
	}
	env, _ = proto.NewEnvelope(proto.TypePermissionRequest, "run", proto.PermissionRequestPayload{RequestID: "permission", Tool: "test"})
	sess.dispatch(env)
	if got, err := reg.LookupPermission("permission"); err != nil || got != sess {
		t.Fatalf("declared interaction identity was not registered: %v", err)
	}
	if got := <-sub.Events; got.ID != "run" {
		t.Fatal("run correlation lost")
	}
}

// jsonMarshal aliases encoding/json.Marshal so call sites read cleanly.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (c *fakeConn) WriteControl(kind int, data []byte, _ time.Time) error {
	return c.WriteMessage(kind, data)
}
