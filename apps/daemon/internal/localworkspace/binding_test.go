package localworkspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func testBinding(t *testing.T) (*Binding, agent.PrepareRequest) {
	t.Helper()
	private := t.TempDir()
	if err := os.Chmod(private, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", private)
	root := t.TempDir()
	environment, session := uuid.NewString(), uuid.NewString()
	b, err := NewWithCapabilityDirectory(environment, session, root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return b, agent.PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{ID: environment, WorkspaceDirectory: "/workspace", CapabilitySources: &agentcapabilities.Input{}}}}
}

func TestBindingRejectsScopeOverrides(t *testing.T) {
	b, prepared := testBinding(t)
	valid := prepared.PromptRequestPayload
	if err := b.Configure(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*proto.PromptRequestPayload){
		"missing reference": func(r *proto.PromptRequestPayload) { r.LocalEnvironment = nil },
		"other Environment": func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment = &proto.LocalEnvironment{ID: uuid.NewString()}
		},
		"none": func(r *proto.PromptRequestPayload) { r.DisableExecutionEnvironment = true },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			mutate(&r)
			if err := b.Configure(r); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
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
