package agentcapabilities

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
)

func TestInstalledCapabilitiesPreserveSourcesWithoutRescanning(t *testing.T) {
	workspace := openTestRoot(t)
	installed := openTestRoot(t)
	inline := agentskill.Metadata{Type: "inline", Name: "inline", Description: "Check proof."}
	if err := writeTree(installed, "skills/inline", []agentbundle.File{{Path: "SKILL.md", Data: skillManifest("inline")}}); err != nil {
		t.Fatal(err)
	}
	plugin := agentplugin.Metadata{Type: "inline", Name: "package", Description: "Package proof."}
	if err := InstallPlugin(installed, 0, testPlugin(t, "bundled"), plugin); err != nil {
		t.Fatal(err)
	}
	writeWorkspace(t, workspace, "generated/nested/SKILL.md", skillManifest("generated"))
	writeWorkspace(t, workspace, "generated/shared/proof.txt", []byte("original-resource"))
	input := Input{Skills: []agentskill.Metadata{inline}, Plugins: []agentplugin.Metadata{plugin}, Directories: []string{"/workspace/generated"}}
	if err := Finalize(installed, input, testIdentity, workspaceResolver(workspace)); err != nil {
		t.Fatal(err)
	}
	before, err := Load(installed)
	if err != nil || len(before.Skills) != 3 {
		t.Fatalf("installed capabilities: %+v %v", before, err)
	}
	if before.Skills[1].RelativeRoot != "plugins/0/skills/bundled" || before.Skills[2].PackageRoot != "directories/0" {
		t.Fatalf("package layout lost: %+v", before)
	}
	if err := workspace.RemoveAll("generated"); err != nil {
		t.Fatal(err)
	}
	after, err := Load(installed)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("load rescanned changed sources: %+v %v", after, err)
	}
	for name, expected := range map[string]string{"plugins/0/shared/proof.txt": "plugin-resource", "directories/0/shared/proof.txt": "original-resource"} {
		body, err := installed.ReadFile(name)
		info, statErr := installed.Stat(name)
		if err != nil || statErr != nil || string(body) != expected || info.Mode().Perm()&0222 != 0 {
			t.Fatalf("snapshot changed or writable: %s", name)
		}
	}
	if Finalize(installed, input, testIdentity, workspaceResolver(workspace)) == nil {
		t.Fatal("completed initialization was replayed")
	}
}

func TestDirectoryDiscoveryDoesNotActivateChildPluginMCP(t *testing.T) {
	workspace, installed := openTestRoot(t), openTestRoot(t)
	writeWorkspace(t, workspace, "parent/child/.codex-plugin/plugin.json", []byte(`{"name":"remote","description":"Remote","skills":"./skills","mcpServers":"./.mcp.json"}`))
	writeWorkspace(t, workspace, "parent/child/.mcp.json", []byte(`{"mcpServers":{"remote":{"type":"http","url":"https://example.com"}}}`))
	writeWorkspace(t, workspace, "parent/child/skills/proof/SKILL.md", skillManifest("proof"))
	if err := Finalize(installed, Input{Directories: []string{"/workspace/parent"}}, testIdentity, workspaceResolver(workspace)); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(installed)
	if err != nil || len(manifest.Skills) != 1 || manifest.Skills[0].RelativeRoot != "directories/0/child/skills/proof" {
		t.Fatalf("parent Skill discovery failed: %+v %v", manifest, err)
	}
	if len(manifest.MCP) != 0 || len(manifest.Plugins) != 0 {
		t.Fatal("parent directory activated child Plugin MCP")
	}
	exact := openTestRoot(t)
	if err := Finalize(exact, Input{Directories: []string{"/workspace/parent/child"}}, testIdentity, workspaceResolver(workspace)); err != nil {
		t.Fatal(err)
	}
	manifest, err = Load(exact)
	if err != nil || len(manifest.MCP) != 1 || manifest.MCP[0].Server.Name != "remote" {
		t.Fatalf("exact Plugin MCP declaration lost: %+v %v", manifest, err)
	}
}

func TestInvalidDirectorySnapshotsNeverPublish(t *testing.T) {
	for _, scenario := range []string{"missing", "outside", "symlink", "duplicate", "file"} {
		t.Run(scenario, func(t *testing.T) {
			workspace, installed := openTestRoot(t), openTestRoot(t)
			writeWorkspace(t, workspace, "first/SKILL.md", skillManifest("proof"))
			input := Input{Directories: []string{"/workspace/first"}}
			switch scenario {
			case "missing":
				input.Directories = []string{"/workspace/missing"}
			case "outside":
				input.Directories = []string{"/workspace/../initialization"}
			case "symlink":
				if err := workspace.Symlink(t.TempDir(), "first/private"); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				writeWorkspace(t, workspace, "second/SKILL.md", skillManifest("proof"))
				input.Directories = append(input.Directories, "/workspace/second")
			case "file":
				input.Directories = []string{"/workspace/first/SKILL.md"}
			}
			if Finalize(installed, input, testIdentity, workspaceResolver(workspace)) == nil {
				t.Fatal("invalid capability source accepted")
			}
			if _, err := installed.Lstat(ManifestName); !os.IsNotExist(err) {
				t.Fatal("failed snapshot published a completion artifact")
			}
		})
	}
}

func openTestRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func writeWorkspace(t *testing.T, root *os.Root, name string, body []byte) {
	t.Helper()
	if root.MkdirAll(filepath.Dir(name), 0700) != nil || root.WriteFile(name, body, 0600) != nil {
		t.Fatal("workspace fixture failed")
	}
}

func skillManifest(name string) []byte {
	return []byte("---\nname: " + name + "\ndescription: Check proof.\n---\nRead supporting files.\n")
}

func testPlugin(t *testing.T, name string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for path, body := range map[string][]byte{
		".codex-plugin/plugin.json":    []byte(`{"name":"package","description":"Package proof.","skills":"./skills"}`),
		"skills/" + name + "/SKILL.md": skillManifest(name),
		"shared/proof.txt":             []byte("plugin-resource"),
	} {
		file, err := writer.Create("package/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
