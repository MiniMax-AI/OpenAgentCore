package localworkspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
)

func markerBinding(t *testing.T) (*Binding, proto.PromptRequestPayload) {
	t.Helper()
	b, request := testBinding(t)
	// This host's inherited ACL gives TempDir group permissions unless cleared.
	if err := os.Chmod(os.Getenv("OAC_RUNTIME_HOME"), 0700); err != nil {
		t.Fatal(err)
	}
	configured, err := b.Configure(request)
	if err != nil {
		t.Fatal(err)
	}
	return b, configured
}
func markerPath(b *Binding) string {
	identity := b.capabilityIdentity()
	return filepath.Join(os.Getenv("OAC_RUNTIME_HOME"), "daemon", "capability-installations", identity.EnvironmentID+"-"+identity.SessionID+".json")
}
func TestSnapshotMarkerRoundTrip(t *testing.T) {
	b, _ := markerBinding(t)
	m, err := b.openSnapshotMarker()
	if err != nil {
		t.Fatal(err)
	}
	if m.completed {
		t.Fatal("fresh marker complete")
	}
	if err := m.complete(); err != nil {
		t.Fatal(err)
	}
	m.close()
	info, err := os.Stat(markerPath(b))
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("marker not private", err)
	}
	raw, err := os.ReadFile(markerPath(b))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 1 || fields["capability_root"] != b.capabilityRoot {
		t.Fatal("marker contains more than installation root")
	}
	m, err = b.openSnapshotMarker()
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	if !m.completed {
		t.Fatal("completion not retained")
	}
	if err := m.complete(); err != nil {
		t.Fatal("completed marker rewritten", err)
	}
}

func TestCompletedSnapshotLossNeverRecapturesSources(t *testing.T) {
	for _, populated := range []bool{false, true} {
		for _, removeRoot := range []bool{false, true} {
			name := map[bool]string{false: "empty", true: "directory"}[populated] + "/" + map[bool]string{false: "manifest", true: "root"}[removeRoot]
			t.Run(name, func(t *testing.T) {
				b, request := markerBinding(t)
				source := t.TempDir()
				if populated {
					writeSourceSkill(t, source, "first")
					request.LocalEnvironment.Capabilities = true
					request.LocalEnvironment.CapabilitySources = &agentcapabilities.Input{Directories: []string{source}}
				}
				if _, err := b.Prepare(t.Context(), request); err != nil {
					t.Fatal(err)
				}
				marker, err := os.ReadFile(markerPath(b))
				if err != nil {
					t.Fatal(err)
				}
				if removeRoot {
					if err := os.RemoveAll(b.capabilityRoot); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Remove(filepath.Join(b.capabilityRoot, agentcapabilities.ManifestName)); err != nil {
					t.Fatal(err)
				}
				if populated {
					writeSourceSkill(t, source, "changed")
				}
				if _, err := b.Prepare(t.Context(), request); err == nil {
					t.Fatal("lost snapshot recreated")
				}
				if _, err := os.Stat(filepath.Join(b.capabilityRoot, agentcapabilities.ManifestName)); !os.IsNotExist(err) {
					t.Fatal("manifest recreated", err)
				}
				if removeRoot {
					if _, err := os.Stat(b.capabilityRoot); !os.IsNotExist(err) {
						t.Fatal("installation root recreated", err)
					}
				}
				after, _ := os.ReadFile(markerPath(b))
				if string(after) != string(marker) {
					t.Fatal("completion evidence changed")
				}
			})
		}
	}
}

func TestSnapshotMarkerBackfillsOnlyVerifiedManifest(t *testing.T) {
	b, request := markerBinding(t)
	source := t.TempDir()
	writeSourceSkill(t, source, "frozen")
	input := agentcapabilities.Input{Directories: []string{source}}
	root, err := os.OpenRoot(b.capabilityRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentcapabilities.Finalize(root, input, b.capabilityIdentity(), b.resolveCapabilityDirectory); err != nil {
		t.Fatal(err)
	}
	root.Close()
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	request.LocalEnvironment.Capabilities = true
	request.LocalEnvironment.CapabilitySources = &input
	if _, err := b.Prepare(t.Context(), request); err != nil {
		t.Fatal("valid manifest was not recovered without sources", err)
	}
	if _, err := os.Stat(markerPath(b)); err != nil {
		t.Fatal("verified completion not recorded", err)
	}
	previousRoot := b.capabilityRoot
	b.capabilityRoot = t.TempDir()
	if _, err := b.Prepare(t.Context(), request); err == nil {
		t.Fatal("operator root changed after completion")
	}
	if _, err := os.Stat(filepath.Join(b.capabilityRoot, agentcapabilities.ManifestName)); !os.IsNotExist(err) {
		t.Fatal("replacement root installed", err)
	}
	b.capabilityRoot = previousRoot
}

func TestSnapshotMarkerRefusesDamagedState(t *testing.T) {
	for _, kind := range []string{"corrupt", "foreign root"} {
		t.Run(kind, func(t *testing.T) {
			b, request := markerBinding(t)
			if _, err := b.Prepare(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			name := markerPath(b)
			switch kind {
			case "corrupt":
				if err := os.WriteFile(name, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign root":
				if err := os.WriteFile(name, []byte("{\"capability_root\":\"/another\"}\n"), 0600); err != nil {
					t.Fatal(err)
				}

			}
			before, _ := os.ReadFile(name)
			if _, err := b.Prepare(t.Context(), request); err == nil {
				t.Fatal("invalid completion evidence accepted")
			}
			after, _ := os.ReadFile(name)
			if string(before) != string(after) {
				t.Fatal("invalid evidence overwritten")
			}
		})
	}
}

func TestCapabilityFinalizeRecordsCompletionAndRejectsLaterImports(t *testing.T) {
	b, _ := markerBinding(t)
	identity := b.capabilityIdentity()
	finalize := proto.RuntimePreparePayload{Step: "begin", EnvironmentID: identity.EnvironmentID, SessionID: identity.SessionID, Action: "finalize", Sources: &agentcapabilities.Input{}}
	if err := b.ApplyRuntimePreparation(t.Context(), finalize, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(markerPath(b)); err != nil {
		t.Fatal("finalize omitted marker", err)
	}
	// Even a fresh empty root cannot make an already completed identity writable.
	if err := os.RemoveAll(b.capabilityRoot); err != nil {
		t.Fatal(err)
	}
	skill := proto.RuntimePreparePayload{Step: "begin", EnvironmentID: identity.EnvironmentID, SessionID: identity.SessionID, Action: "skill", Skill: &agentskill.Metadata{Type: "inline", Name: "example", Description: "Example"}, SizeBytes: 1, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := b.ApplyRuntimePreparation(t.Context(), skill, []byte("x")); err == nil {
		t.Fatal("completed identity accepted import")
	}
	if err := b.ApplyRuntimePreparation(t.Context(), finalize, nil); err == nil {
		t.Fatal("completed identity finalized again")
	}
	if _, err := os.Stat(b.capabilityRoot); !os.IsNotExist(err) {
		t.Fatal("rejected operation recreated root", err)
	}
}

func TestSnapshotMarkerCancellationDoesNotPublishCompletion(t *testing.T) {
	b, request := markerBinding(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := b.Prepare(ctx, request); err == nil {
		t.Fatal("cancelled preparation accepted")
	}
	if _, err := os.Stat(markerPath(b)); !os.IsNotExist(err) {
		t.Fatal("cancelled preparation published marker", err)
	}
}
