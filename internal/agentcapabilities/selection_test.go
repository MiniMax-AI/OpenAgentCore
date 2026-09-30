package agentcapabilities

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
)

var testIdentity = Identity{
	EnvironmentID: "a1000000-0000-4000-8000-000000000001",
	SessionID:     "a1000000-0000-4000-8000-000000000002",
}

// The fixture maps declared managed paths; Finalize knows nothing about it.
func workspaceResolver(workspace *os.Root) DirectoryResolver {
	return func(source string) (*os.Root, error) {
		if ValidateDirectories([]string{source}) != nil {
			return nil, ErrInvalid
		}
		name := strings.TrimPrefix(strings.TrimPrefix(source, "/workspace"), "/")
		if name == "" {
			name = "."
		}
		info, err := workspace.Lstat(name)
		if err != nil || !info.IsDir() {
			return nil, ErrInvalid
		}
		return workspace.OpenRoot(name)
	}
}

func testArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		file, err := writer.Create("package/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestBundleAndLocalDirectoryInventoriesAgree(t *testing.T) {
	for _, plugin := range []bool{false, true} {
		name := "skill"
		if plugin {
			name = "plugin"
		}
		t.Run(name, func(t *testing.T) {
			bundled, local, source := openTestRoot(t), openTestRoot(t), openTestRoot(t)
			files := map[string][]byte{"SKILL.md": skillManifest("proof"), "resource.txt": []byte("preserved")}
			skill := agentskill.Metadata{Type: "inline", Name: "proof", Description: "Check proof."}
			input := Input{Skills: []agentskill.Metadata{skill}}
			if plugin {
				files = map[string][]byte{
					".codex-plugin/plugin.json": []byte(`{"name":"package","description":"Package proof.","skills":"./skills","mcpServers":"./.mcp.json"}`),
					".mcp.json":                 []byte(`{"mcpServers":{"proof":{"command":"python3","args":["proof.py"]}}}`),
					"skills/proof/SKILL.md":     skillManifest("proof"),
					"proof.py":                  []byte("# Preserved server.\n"),
				}
				metadata := agentplugin.Metadata{Type: "inline", Name: "package", Description: "Package proof."}
				input = Input{Plugins: []agentplugin.Metadata{metadata}}
				if err := InstallPlugin(bundled, 0, testArchive(t, files), metadata); err != nil {
					t.Fatal(err)
				}
			} else if err := InstallSkill(bundled, testArchive(t, files), skill); err != nil {
				t.Fatal(err)
			}
			for path, content := range files {
				writeWorkspace(t, source, path, content)
			}
			localInput := Input{Directories: []string{"/caller/capabilities"}}
			resolve := func(path string) (*os.Root, error) {
				if path != "/caller/capabilities" {
					t.Fatalf("unexpected source: %s", path)
				}
				return source.OpenRoot(".")
			}
			if err := Finalize(bundled, input, testIdentity, nil); err != nil {
				t.Fatal(err)
			}
			if err := Finalize(local, localInput, testIdentity, resolve); err != nil {
				t.Fatal(err)
			}
			left, err := Load(bundled)
			if err != nil {
				t.Fatal(err)
			}
			right, err := Load(local)
			if err != nil {
				t.Fatal(err)
			}
			if len(left.Skills) != 1 || len(right.Skills) != 1 || left.Skills[0].Metadata != right.Skills[0].Metadata {
				t.Fatal("Skill inventories diverged")
			}
			if len(left.MCP) != len(right.MCP) {
				t.Fatal("MCP inventories diverged")
			}
			for i := range left.MCP {
				if !reflect.DeepEqual(left.MCP[i].Server, right.MCP[i].Server) {
					t.Fatal("MCP declarations diverged")
				}
			}
			if ValidateSelection(left, input, testIdentity) != nil || ValidateSelection(right, localInput, testIdentity) != nil {
				t.Fatal("matching selection rejected")
			}
		})
	}
}

func TestSnapshotIdentityAndSourceLifetime(t *testing.T) {
	source, first, second := openTestRoot(t), openTestRoot(t), openTestRoot(t)
	writeWorkspace(t, source, "SKILL.md", skillManifest("proof"))
	writeWorkspace(t, source, "resource.txt", []byte("first"))
	input := Input{Directories: []string{"/caller/source"}}
	resolve := func(string) (*os.Root, error) { return source.OpenRoot(".") }
	if err := Finalize(first, input, testIdentity, resolve); err != nil {
		t.Fatal(err)
	}
	writeWorkspace(t, source, "resource.txt", []byte("second"))
	before, err := Load(first)
	if err != nil {
		t.Fatal(err)
	}
	if content, err := first.ReadFile("directories/0/resource.txt"); err != nil || string(content) != "first" {
		t.Fatal("old snapshot followed source edit")
	}
	next := testIdentity
	next.SessionID = "a1000000-0000-4000-8000-000000000003"
	for _, scenario := range []string{"session", "environment", "selection"} {
		identity, selection := testIdentity, input
		switch scenario {
		case "session":
			identity = next
		case "environment":
			identity.EnvironmentID = next.SessionID
		case "selection":
			selection.Directories = []string{"/caller/other"}
		}
		if !errors.Is(ValidateSelection(before, selection, identity), ErrInvalid) {
			t.Fatalf("%s mismatch accepted", scenario)
		}
	}
	if err := Finalize(second, input, next, resolve); err != nil {
		t.Fatal(err)
	}
	if content, err := second.ReadFile("directories/0/resource.txt"); err != nil || string(content) != "second" {
		t.Fatal("new Session did not capture new source bytes")
	}
}

func TestEmptySnapshotAndRequiredBinding(t *testing.T) {
	installed := openTestRoot(t)
	if err := Finalize(installed, Input{}, testIdentity, nil); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(installed)
	if err != nil || len(manifest.Skills) != 0 || len(manifest.MCP) != 0 {
		t.Fatalf("empty snapshot: %+v %v", manifest, err)
	}
	if ValidateSelection(manifest, Input{Skills: []agentskill.Metadata{}, Plugins: []agentplugin.Metadata{}, Directories: []string{}}, testIdentity) != nil {
		t.Fatal("nil/empty selections differ")
	}
	for _, scenario := range []string{"identity", "hash", "hash-uppercase", "old-manifest"} {
		t.Run(scenario, func(t *testing.T) {
			invalid := manifest
			switch scenario {
			case "identity":
				invalid.Identity = Identity{}
			case "hash":
				invalid.SelectionSHA256 = "bad"
			case "hash-uppercase":
				invalid.SelectionSHA256 = strings.ToUpper(invalid.SelectionSHA256)
			case "old-manifest":
				invalid.Identity, invalid.SelectionSHA256 = Identity{}, ""
			}
			root := openTestRoot(t)
			data, err := json.Marshal(invalid)
			if err != nil {
				t.Fatal(err)
			}
			if err := root.WriteFile(ManifestName, data, 0400); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid binding loaded")
			}
		})
	}
	skill := agentskill.Metadata{Type: "inline", Name: "proof", Description: "Check proof."}
	if InstallSkill(installed, testArchive(t, map[string][]byte{"SKILL.md": skillManifest("proof")}), skill) == nil {
		t.Fatal("completed snapshot accepted a late import")
	}
}

func TestPartialAndOversizedSnapshotsFailClosed(t *testing.T) {
	for _, scenario := range []string{"temporary-manifest", "partial-directory", "unexpected-bundle", "missing-bundle", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			source, installed := openTestRoot(t), openTestRoot(t)
			writeWorkspace(t, source, "SKILL.md", skillManifest("proof"))
			input := Input{Directories: []string{"/caller/source"}}
			switch scenario {
			case "temporary-manifest":
				writeWorkspace(t, installed, ManifestName+".tmp", []byte("partial"))
			case "partial-directory":
				writeWorkspace(t, installed, "directories/0/partial", []byte("partial"))
			case "unexpected-bundle":
				writeWorkspace(t, installed, "skills/other/SKILL.md", skillManifest("other"))
			case "missing-bundle":
				input.Skills = []agentskill.Metadata{{Type: "inline", Name: "missing", Description: "Check proof."}}
			case "oversized":
				file, err := source.OpenFile("large", os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(agentbundle.MaxExpandedBytes + 1); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			resolve := func(string) (*os.Root, error) { calls++; return source.OpenRoot(".") }
			if err := Finalize(installed, input, testIdentity, resolve); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid snapshot finalized")
			}
			if _, err := Load(installed); !errors.Is(err, ErrInvalid) {
				t.Fatal("partial installation loaded")
			}
			if _, err := installed.Lstat(ManifestName); !os.IsNotExist(err) {
				t.Fatal("failure published installed.json")
			}
			if scenario == "partial-directory" && calls != 0 {
				t.Fatal("partial installation recaptured sources")
			}
		})
	}
}

func TestSourceValidationKeepsManagedBoundary(t *testing.T) {
	if ValidateLocalDirectories([]string{"/home/caller/skills"}) != nil || ValidateDirectories([]string{"/home/caller/skills"}) == nil {
		t.Fatal("local spelling and managed access policy were conflated")
	}
	for _, sources := range [][]string{{"relative"}, {"/one/../two"}, {"/one", "/one"}, {"/one\\two"}, {"/one\n"}} {
		if ValidateLocalDirectories(sources) == nil {
			t.Fatalf("invalid source accepted: %q", sources)
		}
	}
}

func TestInstallerRejectsSourceRootSymlinksAndInvalidSkills(t *testing.T) {
	source, installed := openTestRoot(t), openTestRoot(t)
	writeWorkspace(t, source, "real/SKILL.md", skillManifest("proof"))
	if err := source.Symlink("real", "alias"); err != nil {
		t.Fatal(err)
	}
	if Finalize(installed, Input{Directories: []string{"/workspace/alias"}}, testIdentity, workspaceResolver(source)) == nil {
		t.Fatal("source alias accepted")
	}
	metadata := agentskill.Metadata{Type: "inline", Name: "proof", Description: "Check proof."}
	if InstallSkill(installed, testArchive(t, map[string][]byte{"SKILL.md": skillManifest("different")}), metadata) == nil {
		t.Fatal("mismatched Skill metadata installed")
	}
	if _, err := installed.Lstat("skills"); !os.IsNotExist(err) {
		t.Fatal("invalid Skill wrote installation data")
	}
}

func TestInstallRejectsAliasedParent(t *testing.T) {
	root := openTestRoot(t)
	if err := root.Mkdir("other", 0700); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("other", "skills"); err != nil {
		t.Fatal(err)
	}
	metadata := agentskill.Metadata{Type: "inline", Name: "proof", Description: "Check proof."}
	if InstallSkill(root, testArchive(t, map[string][]byte{"SKILL.md": skillManifest("proof")}), metadata) == nil {
		t.Fatal("aliased installation parent accepted")
	}
	if _, err := root.Lstat("other/proof"); !os.IsNotExist(err) {
		t.Fatal("installer wrote through aliased parent")
	}
}

func TestFailedDirectoryCaptureCannotBeReplayed(t *testing.T) {
	source, installed := openTestRoot(t), openTestRoot(t)
	writeWorkspace(t, source, "first/SKILL.md", skillManifest("first"))
	input := Input{Directories: []string{"/workspace/first", "/workspace/second"}}
	if Finalize(installed, input, testIdentity, workspaceResolver(source)) == nil {
		t.Fatal("missing second source accepted")
	}
	writeWorkspace(t, source, "first/SKILL.md", skillManifest("changed"))
	writeWorkspace(t, source, "second/SKILL.md", skillManifest("second"))
	if Finalize(installed, input, testIdentity, workspaceResolver(source)) == nil {
		t.Fatal("partial capture was silently retried")
	}
	body, err := installed.ReadFile("directories/0/SKILL.md")
	if err != nil || !bytes.Equal(body, skillManifest("first")) {
		t.Fatal("partial capture was replaced")
	}
}

func TestSelectionDigestKeepsSourceOrderAndMetadata(t *testing.T) {
	input := Input{Directories: []string{"/one", "/two"}, Skills: []agentskill.Metadata{{Type: "inline", Name: "proof", Description: "Check proof."}}}
	digest, err := selectionHash(input)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Version: 1, Identity: testIdentity, SelectionSHA256: digest}
	swapped := input
	swapped.Directories = []string{"/two", "/one"}
	changed := input
	changed.Skills = []agentskill.Metadata{{Type: "inline", Name: "proof", Description: "Changed."}}
	for _, selection := range []Input{swapped, changed} {
		if ValidateSelection(manifest, selection, testIdentity) == nil {
			t.Fatal("changed selection accepted")
		}
	}
}
