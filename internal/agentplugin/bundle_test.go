package agentplugin

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"testing"
)

func TestPluginPreservesMultipleSkillRootsAndSharedResources(t *testing.T) {
	files := pluginFixture()
	files[".mcp.json"] = []byte(`{"mcpServers":{}}`)
	bundle, err := Read(pluginArchive(t, files, 0600), Metadata{Type: "inline", Name: "proof", Description: "Use shared resources."})
	if err != nil || len(bundle.Skills) != 2 {
		t.Fatalf("plugin rejected: %v", err)
	}
	if bundle.Skills[0].RelativeRoot != "skills/first" || bundle.Skills[1].RelativeRoot != "skills/second" {
		t.Fatalf("Skill roots changed: %+v", bundle.Skills)
	}
	found := false
	for _, file := range bundle.Files {
		if file.Path == "references/proof.txt" && string(file.Data) == "package-resource" {
			found = true
		}
	}
	if !found {
		t.Fatal("package-relative resource was dropped")
	}
	if _, err := Read(pluginArchive(t, files, 0600), Metadata{Type: "inline", Name: "other", Description: "Use shared resources."}); err == nil {
		t.Fatal("mismatched public identity accepted")
	}
}

func TestPluginRejectsUnqualifiedActivationAndUnsafeLayout(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"declared MCP", ".codex-plugin/plugin.json", `{"name":"proof","description":"Use shared resources.","skills":"./skills","mcpServers":"./missing.json"}`},
		{"default MCP", ".mcp.json", `{"mcpServers":{"remote":{"type":"http","url":"https://example.com/mcp"}}}`},
		{"hooks", ".codex-plugin/plugin.json", `{"name":"proof","description":"Use shared resources.","skills":"./skills","hooks":"./hooks.json"}`},
		{"escaping declaration", ".codex-plugin/plugin.json", `{"name":"proof","description":"Use shared resources.","skills":"./../private"}`},
		{"absolute declaration", ".codex-plugin/plugin.json", `{"name":"proof","description":"Use shared resources.","skills":"/private"}`},
		{"escaping member", "../../private", "outside"},
		{"duplicate name", "skills/second/SKILL.md", "---\nname: first\ndescription: Read shared resources.\n---\nText"},
		{"native Skill authority", "skills/first/SKILL.md", "---\nname: first\ndescription: Read shared resources.\nallowed-tools: Bash\n---\nText"},
		{"missing selected root", ".codex-plugin/plugin.json", `{"name":"proof","description":"Use shared resources.","skills":"./missing"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := pluginFixture()
			files[tc.path] = []byte(tc.body)
			if _, err := Read(pluginArchive(t, files, 0600), Metadata{Type: "inline", Name: "proof", Description: "Use shared resources."}); err == nil {
				t.Fatal("invalid Plugin accepted")
			}
		})
	}
	if _, err := Read(pluginArchive(t, pluginFixture(), fs.ModeSymlink|0600), Metadata{Type: "inline", Name: "proof", Description: "Use shared resources."}); err == nil {
		t.Fatal("symlink members accepted")
	}
}

func pluginFixture() map[string][]byte {
	return map[string][]byte{
		".codex-plugin/plugin.json": []byte(`{"name":"proof","description":"Use shared resources.","version":"1.0.0","skills":"./skills/"}`),
		"skills/first/SKILL.md":     []byte("---\nname: first\ndescription: Read shared resources.\n---\nRead ../../references/proof.txt.\n"),
		"skills/second/SKILL.md":    []byte("---\nname: second\ndescription: Check shared resources.\n---\nRead ../../references/proof.txt.\n"),
		"references/proof.txt":      []byte("package-resource"),
	}
}

func pluginArchive(t *testing.T, files map[string][]byte, mode fs.FileMode) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for name, body := range files {
		header := &zip.FileHeader{Name: "package/" + name, Method: zip.Deflate}
		header.SetMode(mode)
		file, err := writer.CreateHeader(header)
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
	return out.Bytes()
}
