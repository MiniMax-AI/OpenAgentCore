package localworkspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func writeSourceSkill(t *testing.T, directory, text string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: local-proof\ndescription: Local preparation proof\n---\n"+text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPreparationFreezesLocalContentsAcrossReconnect(t *testing.T) {
	b, req := testBinding(t)
	source := t.TempDir()
	writeSourceSkill(t, source, "first")
	req.LocalEnvironment.CapabilitySources = &agentcapabilities.Input{Directories: []string{source}}
	err := b.Configure(req.PromptRequestPayload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(b.capabilityRoot, agentcapabilities.ManifestName)); !os.IsNotExist(err) {
		t.Fatal("binding installed before asynchronous admission")
	}
	first, err := b.Prepare(t.Context(), req)
	if err != nil || len(first.Skills) != 1 {
		t.Fatalf("first preparation: %v", err)
	}
	writeSourceSkill(t, source, "second")
	reconnect, err := NewWithCapabilityDirectory(b.environment, b.capabilityIdentity().SessionID, b.workspace, b.capabilityRoot)
	if err != nil {
		t.Fatal(err)
	}
	reconnect.networkAccess = b.networkAccess
	again, err := reconnect.Prepare(t.Context(), req)
	if err != nil || len(again.Skills) != 1 {
		t.Fatalf("reconnection: %v", err)
	}
	frozen, err := os.ReadFile(filepath.Join(b.capabilityRoot, again.Skills[0].RelativeRoot, "SKILL.md"))
	if err != nil || string(frozen[len(frozen)-5:]) != "first" {
		t.Fatal("reconnection recaptured source", err)
	}
	next, nextReq := testBinding(t)
	nextReq.LocalEnvironment.CapabilitySources = req.LocalEnvironment.CapabilitySources
	if err := next.Configure(nextReq.PromptRequestPayload); err != nil {
		t.Fatal(err)
	}
	if _, err = next.Prepare(t.Context(), nextReq); err != nil {
		t.Fatal(err)
	}
	fresh, err := os.ReadFile(filepath.Join(next.capabilityRoot, "directories/0/SKILL.md"))
	if err != nil || string(fresh[len(fresh)-6:]) != "second" {
		t.Fatal("new Session did not capture new source", err)
	}
	// Reusing a snapshot under another identity or selection cannot start native work.
	reconnect.session = uuid.NewString()
	if _, err = reconnect.Prepare(t.Context(), req); err == nil {
		t.Fatal("foreign snapshot accepted")
	}
	changed := req
	local := *req.LocalEnvironment
	local.CapabilitySources = &agentcapabilities.Input{}
	changed.LocalEnvironment = &local
	if _, err = b.Prepare(t.Context(), changed); err == nil {
		t.Fatal("changed selection accepted")
	}
}

func TestPreparationUsesOperatorSourcesAndLeavesFailuresInert(t *testing.T) {
	b, req := testBinding(t)
	private := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", private)
	writeSourceSkill(t, private, "private")
	held, err := b.resolveCapabilityDirectory(private)
	if err != nil {
		t.Fatal("operator source rejected", err)
	}
	held.Close()
	if held, err = b.resolveCapabilityDirectory(b.capabilityRoot); err == nil {
		held.Close()
		t.Fatal("recursive snapshot source accepted")
	}
	source := filepath.Join(b.workspace, "selected")
	writeSourceSkill(t, source, "valid")
	root, err := b.resolveCapabilityDirectory("/workspace/selected")
	if err != nil {
		t.Fatal("logical workspace source rejected", err)
	}
	root.Close()
	req.LocalEnvironment.CapabilitySources = &agentcapabilities.Input{Directories: []string{source, t.TempDir()}}
	err = b.Configure(req.PromptRequestPayload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Prepare(t.Context(), req); err == nil {
		t.Fatal("invalid second source accepted")
	}
	if _, err = os.Stat(filepath.Join(b.capabilityRoot, agentcapabilities.ManifestName)); !os.IsNotExist(err) {
		t.Fatal("partial snapshot ready")
	}
	if _, err = b.Prepare(t.Context(), req); err == nil {
		t.Fatal("partial snapshot silently replayed")
	}
}

func TestPreparationEmptySelectionAndCancellation(t *testing.T) {
	b, req := testBinding(t)
	err := b.Configure(req.PromptRequestPayload)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = b.Prepare(ctx, req); err == nil {
		t.Fatal("cancelled preparation started")
	}
	if _, err = b.Prepare(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	read := agent.PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{WorkspaceReadOnly: true}}
	if _, err = b.Prepare(t.Context(), read); err != nil {
		t.Fatal("Files required capability installation", err)
	}
}

func TestRuntimePreparationRejectsMissingRequiredToolEnvironment(t *testing.T) {
	b, req := testBinding(t)
	t.Setenv("OAC_RUNTIME_INITIALIZATION_DIRECTORY", t.TempDir())
	t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", "")
	req.LocalEnvironment.ToolEnvironment = true
	err := b.Configure(req.PromptRequestPayload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Prepare(t.Context(), req); err == nil {
		t.Fatal("missing required tool environment admitted")
	}
	directory, err := InitializationDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "tool-env.json"), []byte(`{"READY":"yes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Prepare(t.Context(), req); err != nil {
		t.Fatal("prepared tool environment rejected", err)
	}
	if err = os.Remove(filepath.Join(directory, "tool-env.json")); err != nil {
		t.Fatal(err)
	}
	req.LocalEnvironment.ToolEnvironment = false
	if _, err = b.Prepare(t.Context(), req); err == nil {
		t.Fatal("deleted prepared tool environment was silently recreated")
	}
}

func TestRuntimePreparationRejectsMissingExplicitToolEnvironment(t *testing.T) {
	t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := ReadOptionalToolEnvironment(); err == nil {
		t.Fatal("missing explicitly configured tool environment ignored")
	}
}

func TestPreparationFreezesToolOnlyEnvironmentAcrossReconnect(t *testing.T) {
	for _, initialFile := range []bool{false, true} {
		t.Run(fmt.Sprint(initialFile), func(t *testing.T) {
			b, req := testBinding(t)
			t.Setenv("OAC_RUNTIME_INITIALIZATION_DIRECTORY", t.TempDir())
			t.Setenv("OAC_RUNTIME_PACKAGE_DIRECTORY", t.TempDir())
			source := filepath.Join(t.TempDir(), "operator.json")
			if err := os.WriteFile(source, []byte(`{"LOCAL_ONLY":"original"}`), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", source)
			if initialFile {
				if err := b.installInitialFile(t.Context(), proto.RuntimeInitialFile{Path: "/workspace/input"}, []byte("file")); err != nil {
					t.Fatal(err)
				}
			}
			if err := b.Configure(req.PromptRequestPayload); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Prepare(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte(`{"LOCAL_ONLY":"changed"}`), 0600); err != nil {
				t.Fatal(err)
			}
			reconnect, err := NewWithCapabilityDirectory(b.environment, b.capabilityIdentity().SessionID, b.workspace, b.capabilityRoot)
			if err != nil {
				t.Fatal(err)
			}
			reconnect.networkAccess = b.networkAccess
			if _, err := reconnect.Prepare(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			values, err := ReadOptionalToolEnvironment()
			if err != nil || values["LOCAL_ONLY"] != "original" {
				t.Fatal("reconnect reread mutable source", err)
			}
		})
	}
}
