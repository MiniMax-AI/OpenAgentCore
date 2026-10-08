package dispatch_test

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/google/uuid"
)

// testOwner is the Environment owner of one Session in one Environment, as
// the agent host's is. Its workspace is in memory: a path maps to a file's
// bytes, or to nil for a directory. prepare, when set, replaces Prepare's
// result.
type testOwner struct {
	environment, session string
	prepare              func(agent.PrepareRequest) (agent.PrepareRequest, error)
	mu                   sync.Mutex
	files                map[string][]byte
}

func newTestOwner(environment, session string) *testOwner {
	return &testOwner{environment: environment, session: session, files: map[string][]byte{}}
}

// Resolve serves the owner's Session in its Environment and no other.
func (o *testOwner) Resolve(ref proto.AssignmentRef, bind proto.AssignmentBindPayload) dispatch.Environment {
	if bind.EnvironmentID != o.environment || ref.SessionID != o.session {
		return nil
	}
	return o
}

// put adds a file, or a directory when data is nil.
func (o *testOwner) put(name string, data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.files[name] = data
}

// file returns the file at name.
func (o *testOwner) file(name string) ([]byte, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	data, ok := o.files[name]
	return data, ok && data != nil
}

func (o *testOwner) Configure(r proto.PromptRequestPayload) error {
	local := r.LocalEnvironment
	switch {
	case local == nil || r.DisableExecutionEnvironment || local.ID != o.environment:
		return errors.New("the request does not name the Session's Environment")
	case r.WorkspaceReadOnly:
		return nil
	case local.CapabilitySources == nil || agentcapabilities.ValidateInput(*local.CapabilitySources) != nil:
		return agentcapabilities.ErrInvalid
	}
	return nil
}

func (o *testOwner) Prepare(_ context.Context, r agent.PrepareRequest) (agent.PrepareRequest, error) {
	if o.prepare != nil {
		return o.prepare(r)
	}
	r.WorkspaceRoot = "/workspace"
	return r, nil
}

func (o *testOwner) ApplyRuntimePreparation(context.Context, uuid.UUID, proto.RuntimePreparePayload, []byte) error {
	return dispatch.ErrEnvironmentUnavailable
}

func (o *testOwner) ListWorkspaceDirectory(_ context.Context, dir string, limit int) (dispatch.WorkspaceDirectoryResult, error) {
	if limit < 1 || limit > proto.WorkspaceDirectoryMaxEntries || dir != "" && !proto.ValidWorkspacePath(dir) {
		return dispatch.WorkspaceDirectoryResult{}, dispatch.ErrWorkspaceReadInvalid
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if data, ok := o.files[dir]; dir != "" && (!ok || data != nil) {
		return dispatch.WorkspaceDirectoryResult{}, dispatch.ErrWorkspaceNotDirectory
	}
	parent := dir
	if parent == "" {
		parent = "."
	}
	result := dispatch.WorkspaceDirectoryResult{Entries: []dispatch.WorkspaceDirectoryEntry{}}
	for name, data := range o.files {
		if path.Dir(name) != parent {
			continue
		}
		entry := dispatch.WorkspaceDirectoryEntry{Name: path.Base(name), Kind: "directory"}
		if data != nil {
			size := int64(len(data))
			entry.Kind, entry.SizeBytes = "file", &size
		}
		result.Entries = append(result.Entries, entry)
	}
	slices.SortFunc(result.Entries, func(a, b dispatch.WorkspaceDirectoryEntry) int { return strings.Compare(a.Name, b.Name) })
	if len(result.Entries) > limit {
		result.Entries, result.Truncated = result.Entries[:limit], true
	}
	return result, nil
}

// WriteWorkspaceFile creates a new file below plain directories, as Files.create does.
func (o *testOwner) WriteWorkspaceFile(_ context.Context, name string, data []byte) (dispatch.WorkspaceWriteResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if existing, ok := o.files[name]; ok && existing == nil {
		return dispatch.WorkspaceWriteResult{}, dispatch.ErrWorkspaceWriteDirectory
	} else if ok {
		return dispatch.WorkspaceWriteResult{}, dispatch.ErrWorkspaceWriteUnsafe
	}
	for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
		if data, ok := o.files[parent]; ok && data != nil {
			return dispatch.WorkspaceWriteResult{}, dispatch.ErrWorkspaceWriteRejected
		}
	}
	o.files[name] = append([]byte{}, data...)
	return dispatch.WorkspaceWriteResult{SizeBytes: int64(len(data))}, nil
}

// ExportOutputs exports no outputs.
func (o *testOwner) ExportOutputs(_ context.Context, w io.Writer) error {
	return tar.NewWriter(w).Close()
}

func (o *testOwner) Close(context.Context) error { return nil }

// assertPreparationOutcome waits for the terminal admission status of id. An
// admitted request fails in the controlled factory; a rejected one sends only
// its rejection.
func assertPreparationOutcome(t *testing.T, sender *recSender, id string, admitted bool) proto.PreparationStatusPayload {
	t.Helper()
	state, frames := "rejected", 1
	if admitted {
		state, frames = "failed", 2
	}
	status := waitPreparationStatus(t, sender, id, state, "")
	if got := sender.typesFor(id); len(got) != frames {
		t.Fatalf("preparation %s frames = %v, want %d status frames", id, got, frames)
	}
	return status
}

func TestNoEnvironmentRejectsOtherEngineBeforeFactory(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	var called atomic.Bool
	registerExecutorKind(h.reg, proto.SupportedAgentKind{Kind: "fake_alpha", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, func(context.Context, agent.PrepareRequest) (agent.Executor, error) {
		called.Store(true)
		return nil, errors.New("controlled factory stop")
	})
	assign(t, h.router, preparationSessionID, "")
	err := h.router.Handle(context.Background(), mustEnv(t, proto.TypeExecutionPrepare, "none", noEnvironmentPreparation(preparationSessionID, proto.PromptRequestPayload{AgentKind: "fake_alpha"})))
	if err == nil {
		t.Fatal("unsupported engine was admitted")
	}
	if status := assertPreparationOutcome(t, h.sender, "none", false); status.ErrorCode != "unsupported_configuration" || called.Load() {
		t.Fatalf("unsupported engine was started: status=%+v called=%t", status, called.Load())
	}
}

func TestNoEnvironmentUsesAvailableCapability(t *testing.T) {
	for _, available := range []bool{false, true} {
		h := newHarness(t)
		defer h.router.Shutdown(context.Background())
		var called atomic.Bool
		registerExecutorKind(h.reg, proto.SupportedAgentKind{Kind: "claude_sdk", Available: available, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})}, func(context.Context, agent.PrepareRequest) (agent.Executor, error) {
			called.Store(true)
			return nil, errors.New("controlled factory stop")
		})
		assign(t, h.router, preparationSessionID, "")
		_ = h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "sdk", noEnvironmentPreparation(preparationSessionID, proto.PromptRequestPayload{AgentKind: "claude_sdk"})))
		assertPreparationOutcome(t, h.sender, "sdk", available)
		if called.Load() != available {
			t.Fatalf("factory called=%t, available=%t", called.Load(), available)
		}
	}
}

func TestLocalEnvironmentRequiresAvailableCapability(t *testing.T) {
	for _, mode := range []string{"unsupported", "unavailable", "none conflict", "supported"} {
		t.Run(mode, func(t *testing.T) {
			h := localPreparationHarness(t)
			defer h.router.Shutdown(context.Background())
			var called atomic.Bool
			registerExecutorKind(h.reg, proto.SupportedAgentKind{Kind: "codex", Available: mode != "unavailable",
				Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilityFromBool(mode != "unsupported")})},
				func(_ context.Context, req agent.PrepareRequest) (agent.Executor, error) {
					called.Store(true)
					if req.LocalEnvironment == nil || req.LocalEnvironment.ID != preparationEnvironmentID {
						t.Error("local descriptor lost before factory")
					}
					return nil, errors.New("controlled factory stop")
				})
			req := preparationRequest()
			req.Configuration.AgentKind = "codex"
			req.Configuration.DisableExecutionEnvironment = mode == "none conflict"
			err := h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "local", req))
			if (err == nil) != (mode == "supported") {
				t.Fatalf("wrong admission for %s: %v", mode, err)
			}
			assertPreparationOutcome(t, h.sender, "local", mode == "supported")
			if called.Load() != (mode == "supported") {
				t.Fatalf("unexpected factory call for %s", mode)
			}
		})
	}
}

// The owner resolves installed MCP servers during preparation, and the kind's
// declaration checks them before the factory sees them.
func TestInstalledMCPIsCheckedBeforeTheFactory(t *testing.T) {
	for label, admitted := range map[string]bool{"reserved": false, "installed": true} {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t)
			if err := h.router.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			// The owner installs one MCP server labelled label.
			owner := newTestOwner(preparationEnvironmentID, preparationSessionID)
			owner.prepare = func(req agent.PrepareRequest) (agent.PrepareRequest, error) {
				req.MCP = append(req.MCP, agent.EnvironmentMCP{Server: agentplugin.MCPServer{Name: label, Type: "http", URL: "https://mcp.example"}})
				return req, nil
			}
			var err error
			h.router, err = dispatch.New(dispatch.Config{Registry: h.reg, Sender: h.sender, Environments: owner.Resolve})
			if err != nil {
				t.Fatal(err)
			}
			defer h.router.Shutdown(context.Background())
			assign(t, h.router, preparationSessionID, preparationEnvironmentID)
			configuration := prototest.ModelConfiguration()
			configuration.Declaration.ReservedMCPLabels = []string{"reserved"}
			h.reg.RegisterKind(proto.SupportedAgentKind{Kind: "prepared", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported})}, configuration)
			var called atomic.Bool
			h.reg.RegisterExecutor("prepared", func(context.Context, agent.PrepareRequest) (agent.Executor, error) {
				called.Store(true)
				return nil, errors.New("controlled factory stop")
			})
			if err := h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "installed", preparationRequest())); err != nil {
				t.Fatal(err)
			}
			assertPreparationOutcome(t, h.sender, "installed", true)
			if called.Load() != admitted {
				t.Fatalf("factory called=%t, want %t", called.Load(), admitted)
			}
		})
	}
}

// A Session whose assignment resolves no Environment owner declares no
// Environment operation: each gets its typed rejection before any effect.
func TestSessionWithoutOwnerRejectsEnvironmentOperations(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	var called atomic.Bool
	registerExecutorKind(h.reg, proto.SupportedAgentKind{Kind: "local", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported})}, func(context.Context, agent.PrepareRequest) (agent.Executor, error) {
		called.Store(true)
		return nil, errors.New("controlled factory stop")
	})
	assign(t, h.router, preparationSessionID, preparationEnvironmentID)
	execution := proto.PromptRequestPayload{AgentKind: "local", LocalEnvironment: &proto.LocalEnvironment{ID: preparationEnvironmentID}}
	read := execution
	read.WorkspaceReadOnly = true
	for id, test := range map[string]struct {
		request proto.PromptRequestPayload
		code    string
	}{"read": {read, "unsupported_read_preparation"}, "execution": {execution, "invalid_configuration"}} {
		_ = h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, id, proto.ExecutionPreparePayload{SessionID: preparationSessionID, Configuration: test.request}))
		if status := waitPreparationStatus(t, h.sender, id, "rejected", ""); status.ErrorCode != test.code {
			t.Fatalf("%s preparation = %+v", id, status)
		}
	}
	empty := sha256.Sum256(nil)
	for _, test := range []struct {
		request, result string
		payload         any
		code            string
	}{
		{proto.TypeRuntimePrepare, proto.TypeRuntimePrepareResult, proto.RuntimePreparePayload{Step: "begin", Action: "file", EnvironmentID: preparationEnvironmentID, SessionID: preparationSessionID, File: &proto.RuntimeInitialFile{Path: "/workspace/input"}, SHA256: hex.EncodeToString(empty[:])}, "runtime_preparation_unsupported"},
		{proto.TypeWorkspaceWrite, proto.TypeWorkspaceWriteResult, proto.WorkspaceWritePayload{Step: "begin", EnvironmentID: preparationEnvironmentID, SessionID: preparationSessionID, Path: "file", SHA256: hex.EncodeToString(empty[:])}, "write_unsupported"},
		{proto.TypeWorkspaceRead, proto.TypeWorkspaceReadResult, proto.WorkspaceReadPayload{Handle: "handle", EnvironmentID: preparationEnvironmentID, MaxEntries: 1}, "read_unsupported"},
		{proto.TypeWorkspaceExport, proto.TypeWorkspaceExportResult, proto.WorkspaceExportPayload{Step: "begin", Handle: "handle", EnvironmentID: preparationEnvironmentID}, "read_unsupported"},
	} {
		id := uuid.NewString()
		if err := h.router.Handle(t.Context(), mustEnv(t, test.request, id, test.payload)); err != nil {
			t.Fatal(test.request, err)
		}
		waitFor(t, func() bool { return hasFrame(h.sender, test.result, id) }, test.result)
		frame, _ := frameFor(h.sender, test.result, id)
		var result struct {
			Outcome   string `json:"outcome"`
			ErrorCode string `json:"error_code"`
		}
		if err := frame.DecodePayload(&result); err != nil || result.Outcome != "rejected" || result.ErrorCode != test.code {
			t.Fatalf("%s = %+v, %v", test.request, result, err)
		}
	}
	if called.Load() {
		t.Fatal("an operation reached the Executor factory")
	}
}

func TestOwnedRuntimeRejectsForeignSessionBind(t *testing.T) {
	h := localPreparationHarness(t)
	defer h.router.Shutdown(context.Background())
	const foreign = "33333333-3333-4333-8333-333333333333"
	if err := h.router.Handle(t.Context(), scoped(t, foreign, proto.TypeAssignmentBind, "bind-foreign", proto.AssignmentBindPayload{EnvironmentID: preparationEnvironmentID, WorkspaceDirectory: "/workspace"})); err != nil {
		t.Fatal(err)
	}
	if status := waitAssignmentStatus(t, h.sender, "bind-foreign"); status.ErrorCode != proto.AssignmentConflict {
		t.Fatalf("foreign bind = %+v", status)
	}
}
