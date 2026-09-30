package localworkspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func testBinding(t *testing.T) (*Binding, proto.PromptRequestPayload) {
	t.Helper()
	private := t.TempDir()
	if err := os.Chmod(private, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", private)
	root := t.TempDir()
	environment, session := uuid.NewString(), uuid.NewString()
	b, err := New(environment, session, root)
	if err != nil {
		t.Fatal(err)
	}
	b.networkAccess = "disabled"
	b.capabilityRoot = t.TempDir()
	return b, proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{ID: environment, NetworkAccess: "disabled", WorkspaceDirectory: "/workspace", CapabilitySources: &agentcapabilities.Input{}}, AgentStateKey: "agents-api-" + session, StrictResume: true, ReleaseOnCompletion: true}
}

func TestBindingRejectsScopeAndPathOverrides(t *testing.T) {
	b, valid := testBinding(t)
	configured, err := b.Configure(valid)
	if err != nil || configured.WorkDir != b.workspace {
		t.Fatalf("frozen cwd: %+v %v", configured, err)
	}
	for name, mutate := range map[string]func(*proto.PromptRequestPayload){
		"missing reference": func(r *proto.PromptRequestPayload) { r.LocalEnvironment = nil },
		"other Environment": func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment = &proto.LocalEnvironment{ID: uuid.NewString()}
		},
		"other Session":     func(r *proto.PromptRequestPayload) { r.AgentStateKey = "agents-api-" + uuid.NewString() },
		"path override":     func(r *proto.PromptRequestPayload) { r.WorkDir = b.workspace },
		"none":              func(r *proto.PromptRequestPayload) { r.DisableExecutionEnvironment = true },
		"product authoring": func(r *proto.PromptRequestPayload) { r.WorkspaceAuthoring = true },
		"non-strict resume": func(r *proto.PromptRequestPayload) { r.StrictResume = false },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			mutate(&r)
			if _, err := b.Configure(r); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	if _, err := (*Binding)(nil).Configure(valid); err == nil {
		t.Fatal("unbound Runtime accepted a local Environment")
	}
	if _, err := (*Binding)(nil).Configure(proto.PromptRequestPayload{}); err != nil {
		t.Fatal("ordinary unbound behavior changed", err)
	}
}

func TestDirectoryValidatesRelativePaths(t *testing.T) {
	b, _ := testBinding(t)
	got, err := b.ListWorkspaceDirectory(t.Context(), "", 2)
	if err != nil || got.Entries == nil || len(got.Entries) != 0 || got.Truncated {
		t.Fatalf("empty directory: %+v %v", got, err)
	}
	for _, path := range []string{"/etc", "..", "a/../b", "a//b", ".", "a\\b"} {
		if _, err := b.ListWorkspaceDirectory(t.Context(), path, 2); err == nil {
			t.Fatalf("invalid path accepted: %q", path)
		}
	}
}

func TestBindingAllowsRetainedExecutor(t *testing.T) {
	b, req := testBinding(t)
	req.ReleaseOnCompletion = false
	if _, err := b.Configure(req); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityLayoutUsesOperatorDirectories(t *testing.T) {
	b, _ := testBinding(t)
	for _, directory := range []string{filepath.Join(b.workspace, "capabilities"), filepath.Join(os.Getenv("OAC_RUNTIME_HOME"), "capabilities")} {
		if _, err := NewWithCapabilityDirectory(b.environment, b.capabilityIdentity().SessionID, b.workspace, directory); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewWithCapabilityDirectory(b.environment, b.capabilityIdentity().SessionID, b.workspace, "relative"); err == nil {
		t.Fatal("relative installation path accepted")
	}
}
